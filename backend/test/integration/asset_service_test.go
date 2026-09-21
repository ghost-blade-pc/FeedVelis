package integration

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	assetApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asset"
	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	assetDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/asset"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

// stubStorage 用固定的探测结果替代真实对象存储：本用例验证事务、额度与幂等，
// 真实字节的签名与尺寸识别已由 minio 包对真实 MinIO 的测试覆盖。
type stubStorage struct {
	size    int64
	probe   ports.ImageProbe
	statErr error
	urls    []string
}

func (s *stubStorage) EnsurePrivateBucket(context.Context) error { return nil }

func (s *stubStorage) PresignUpload(_ context.Context, objectKey string, _ time.Duration) (ports.PresignedUpload, error) {
	url := "https://storage.example/" + objectKey + "?signature=" + time.Now().UTC().Format("150405.000000000")
	s.urls = append(s.urls, url)
	return ports.PresignedUpload{URL: url, Method: "PUT", Headers: map[string]string{}}, nil
}

func (s *stubStorage) StatObject(context.Context, string) (ports.ObjectInfo, error) {
	if s.statErr != nil {
		return ports.ObjectInfo{}, s.statErr
	}
	return ports.ObjectInfo{SizeBytes: s.size, ContentType: s.probe.ContentType, ETag: "etag-integration"}, nil
}

func (s *stubStorage) ProbeImage(context.Context, string, int64) (ports.ImageProbe, error) {
	probe := s.probe
	probe.SizeBytes = s.size
	return probe, nil
}

func (s *stubStorage) OpenObject(context.Context, string) (ports.ObjectStream, error) {
	return ports.ObjectStream{Body: io.NopCloser(nil)}, nil
}

func (s *stubStorage) DeleteObject(context.Context, string) error { return nil }

func newAssetService(t *testing.T, env *testEnv, storage *stubStorage, now time.Time) (*assetApp.Service, *postgres.ArticleAssetRepository) {
	t.Helper()
	repository := postgres.NewArticleAssetRepository(env.pool)
	idempotency := idempotencyApp.NewService(postgres.NewIdempotencyRepository(env.pool),
		postgres.NewTxManager(env.pool), 24*time.Hour)
	service := assetApp.NewService(repository, storage, idempotency, &articleTestClock{now: now}, assetApp.Policy{
		Limits: assetLimits, UserQuotaBytes: 1 << 30, PendingLimit: 20,
		UploadTTL: 15 * time.Minute, ProbeBytes: 1 << 20,
	})
	return service, repository
}

func TestAssetCreateReplayKeepsSingleAssetWithFreshUpload(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	now := fixedNow()
	seedAssetUsers(t, env, now)
	storage := &stubStorage{}
	service, _ := newAssetService(t, env, storage, now)
	ctx := context.Background()

	command := assetApp.CreateCommand{OwnerUserID: assetOwnerID,
		IdempotencyKey: "55000000-0000-0000-0000-000000000001",
		ContentType:    assetDomain.ContentTypePNG, SizeBytes: 4096}
	first, replayed, err := service.Create(ctx, command)
	if err != nil || replayed {
		t.Fatalf("首次创建 replayed=%t err=%v", replayed, err)
	}
	second, replayed, err := service.Create(ctx, command)
	if err != nil || !replayed {
		t.Fatalf("重放 replayed=%t err=%v", replayed, err)
	}
	if second.Asset.ID() != first.Asset.ID() {
		t.Fatalf("重放返回了不同资产: %s / %s", second.Asset.ID(), first.Asset.ID())
	}
	// 预签名地址有时效，每次请求都必须重新签发。
	if len(storage.urls) != 2 || storage.urls[0] == storage.urls[1] {
		t.Fatalf("预签名地址 = %v，期望两次不同的有效地址", storage.urls)
	}
	var rows int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.article_assets WHERE owner_user_id=$1`, assetOwnerID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("资产行数 = %d err=%v", rows, err)
	}

	// 同键异请求必须冲突且不新建资产。
	conflicting := command
	conflicting.SizeBytes = 8192
	if _, _, err := service.Create(ctx, conflicting); !errors.Is(err, idempotencyApp.ErrKeyReused) {
		t.Fatalf("同键异请求 err = %v", err)
	}
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.article_assets WHERE owner_user_id=$1`, assetOwnerID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("冲突后资产行数 = %d err=%v", rows, err)
	}
}

func TestAssetConfirmReplayChargesQuotaOnce(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	now := fixedNow()
	seedAssetUsers(t, env, now)
	storage := &stubStorage{size: 2048, probe: ports.ImageProbe{ContentType: assetDomain.ContentTypePNG, Width: 640, Height: 480}}
	service, repository := newAssetService(t, env, storage, now)
	ctx := context.Background()

	created, _, err := service.Create(ctx, assetApp.CreateCommand{OwnerUserID: assetOwnerID,
		IdempotencyKey: "56000000-0000-0000-0000-000000000001",
		ContentType:    assetDomain.ContentTypePNG, SizeBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	confirm := assetApp.ConfirmCommand{OwnerUserID: assetOwnerID, AssetID: created.Asset.ID(),
		IdempotencyKey: "56000000-0000-0000-0000-000000000002"}
	confirmed, replayed, err := service.Confirm(ctx, confirm)
	if err != nil || replayed {
		t.Fatalf("首次确认 replayed=%t err=%v", replayed, err)
	}
	if confirmed.Status() != assetDomain.StatusReady || confirmed.QuotaCountedAt() == nil {
		t.Fatalf("确认结果 = %+v", confirmed)
	}

	// 同键重放：返回同一结果，不再计费。
	replayedAsset, replayed, err := service.Confirm(ctx, confirm)
	if err != nil || !replayed || replayedAsset.ID() != confirmed.ID() {
		t.Fatalf("重放确认 replayed=%t err=%v", replayed, err)
	}
	// 新键再次确认同一资产：仍然只计一次费。
	if _, _, err := service.Confirm(ctx, assetApp.ConfirmCommand{OwnerUserID: assetOwnerID,
		AssetID: created.Asset.ID(), IdempotencyKey: "56000000-0000-0000-0000-000000000003"}); err != nil {
		t.Fatalf("重复确认 err = %v", err)
	}

	usage, err := repository.Usage(ctx, assetOwnerID)
	if err != nil || usage.CountedBytes != 2048 || usage.PendingCount != 0 {
		t.Fatalf("用量 = %+v err=%v", usage, err)
	}
	var counted int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.article_assets
WHERE owner_user_id=$1 AND quota_counted_at IS NOT NULL`, assetOwnerID).Scan(&counted); err != nil || counted != 1 {
		t.Fatalf("计费行数 = %d err=%v", counted, err)
	}
}

func TestAssetConfirmRejectsForeignOwnerWithoutLeaking(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	now := fixedNow()
	seedAssetUsers(t, env, now)
	storage := &stubStorage{size: 1024, probe: ports.ImageProbe{ContentType: assetDomain.ContentTypePNG, Width: 10, Height: 10}}
	service, _ := newAssetService(t, env, storage, now)
	ctx := context.Background()

	created, _, err := service.Create(ctx, assetApp.CreateCommand{OwnerUserID: assetOwnerID,
		IdempotencyKey: "57000000-0000-0000-0000-000000000001",
		ContentType:    assetDomain.ContentTypePNG, SizeBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	// 他人确认与不存在资产返回同一个错误，不区分“存在但非我所有”。
	_, _, err = service.Confirm(ctx, assetApp.ConfirmCommand{OwnerUserID: assetOtherID,
		AssetID: created.Asset.ID(), IdempotencyKey: "57000000-0000-0000-0000-000000000002"})
	if !errors.Is(err, assetDomain.ErrNotFound) {
		t.Fatalf("他人确认 err = %v", err)
	}
	_, _, err = service.Confirm(ctx, assetApp.ConfirmCommand{OwnerUserID: assetOtherID,
		AssetID: "57000000-0000-0000-0000-00000000dead", IdempotencyKey: "57000000-0000-0000-0000-000000000003"})
	if !errors.Is(err, assetDomain.ErrNotFound) {
		t.Fatalf("不存在资产 err = %v", err)
	}
}
