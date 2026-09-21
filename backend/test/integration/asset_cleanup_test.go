package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	assetApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asset"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

const (
	cleanupPendingTTL = 24 * time.Hour
	cleanupUnboundTTL = 7 * 24 * time.Hour
)

// faultStorage 可注入对象删除故障，用于验证清理可重试。
type faultStorage struct {
	failures map[string]error
	deleted  []string
}

func (f *faultStorage) DeleteObject(_ context.Context, objectKey string) error {
	if err, ok := f.failures[objectKey]; ok {
		return err
	}
	f.deleted = append(f.deleted, objectKey)
	return nil
}

func newCleanupService(t *testing.T, env *testEnv, storage *faultStorage, now time.Time) *assetApp.CleanupService {
	t.Helper()
	service, err := assetApp.NewCleanupService(assetApp.CleanupDeps{
		Repository: postgres.NewArticleAssetRepository(env.pool), Storage: storage,
		Clock: &articleTestClock{now: now}, Batch: 50,
		PendingTTL: cleanupPendingTTL, UnboundTTL: cleanupUnboundTTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// seedCleanupAsset 直接写入一条指定状态与时间的资产。
func seedCleanupAsset(t *testing.T, env *testEnv, assetID, status string, boundArticleID *int64, createdAt time.Time) {
	t.Helper()
	ctx := context.Background()
	switch status {
	case "pending":
		if _, err := env.pool.Exec(ctx, `INSERT INTO velis.article_assets
(id,owner_user_id,bound_article_id,object_key,status,delete_requested_at,created_at,updated_at)
VALUES ($1,$2,$3,$4,'pending',$5,$5,$5)`, assetID, assetOwnerID, boundArticleID, "cleanup/"+assetID, createdAt); err != nil {
			t.Fatal(err)
		}
	default:
		if _, err := env.pool.Exec(ctx, `INSERT INTO velis.article_assets
(id,owner_user_id,bound_article_id,object_key,status,content_type,size_bytes,width,height,checksum,
quota_counted_at,confirmed_at,delete_requested_at,deleted_at,created_at,updated_at)
VALUES ($1,$2,$3,$4,$5,'image/png',1024,10,10,'etag',$6,$6,$7,$8,$6,$6)`,
			assetID, assetOwnerID, boundArticleID, "cleanup/"+assetID, status, createdAt, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// assetStatus 返回资产状态；行已被移除时返回空字符串。
func assetStatus(t *testing.T, env *testEnv, assetID string) string {
	t.Helper()
	var status string
	err := env.pool.QueryRow(context.Background(),
		`SELECT status FROM velis.article_assets WHERE id=$1`, assetID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func TestAssetCleanupMarksExpiredAndUnboundAssets(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	now := fixedNow()
	seedAssetUsers(t, env, now)
	storage := &faultStorage{}
	service := newCleanupService(t, env, storage, now)
	ctx := context.Background()

	// 过期待上传、长期未绑定、以及仍在保留期内的对照项。
	seedCleanupAsset(t, env, "5a000000-0000-0000-0000-000000000001", "pending", nil, now.Add(-25*time.Hour))
	seedCleanupAsset(t, env, "5a000000-0000-0000-0000-000000000002", "ready", nil, now.Add(-8*24*time.Hour))
	seedCleanupAsset(t, env, "5a000000-0000-0000-0000-000000000003", "pending", nil, now.Add(-time.Hour))
	seedCleanupAsset(t, env, "5a000000-0000-0000-0000-000000000004", "ready", nil, now.Add(-6*24*time.Hour))

	result, err := service.Run(ctx)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	// 两条删除路径：过期待上传直接删对象并移除行；长期未绑定先标记 delete_pending 再删对象。
	if result.Marked != 1 || result.Deleted != 2 || result.Failed != 0 {
		t.Fatalf("清理结果 = %+v", result)
	}
	// 过期待上传行被移除；长期未绑定的已确认行保留并落为 deleted。
	if status := assetStatus(t, env, "5a000000-0000-0000-0000-000000000001"); status != "" {
		t.Fatalf("过期待上传应被移除，实际状态 %q", status)
	}
	if status := assetStatus(t, env, "5a000000-0000-0000-0000-000000000002"); status != "deleted" {
		t.Fatalf("长期未绑定资产状态 = %q", status)
	}
	for _, assetID := range []string{"5a000000-0000-0000-0000-000000000003", "5a000000-0000-0000-0000-000000000004"} {
		if status := assetStatus(t, env, assetID); status == "delete_pending" || status == "deleted" {
			t.Fatalf("保留期内的 %s 不得被清理，实际状态 %s", assetID, status)
		}
	}
	if len(storage.deleted) != 2 {
		t.Fatalf("对象删除次数 = %v", storage.deleted)
	}

	// 重复执行不重复处置：已删除的资产不再进入候选。
	again, err := service.Run(ctx)
	if err != nil || again.Total() != 0 {
		t.Fatalf("第二轮清理 = %+v err=%v", again, err)
	}
}

// TestAssetCleanupRetriesFailedObjectDeletion 用故障注入验证对象删除失败可重试。
func TestAssetCleanupRetriesFailedObjectDeletion(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	now := fixedNow()
	seedAssetUsers(t, env, now)
	const stuckID = "5b000000-0000-0000-0000-000000000001"
	const healthyID = "5b000000-0000-0000-0000-000000000002"
	storage := &faultStorage{failures: map[string]error{
		"cleanup/" + stuckID: ports.ErrAssetStorageUnavailable,
	}}
	service := newCleanupService(t, env, storage, now)
	ctx := context.Background()

	seedCleanupAsset(t, env, stuckID, "pending", nil, now.Add(-25*time.Hour))
	seedCleanupAsset(t, env, healthyID, "pending", nil, now.Add(-25*time.Hour))

	first, err := service.Run(ctx)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	// 单条失败不中断整轮：另一条照常删除。
	if first.Marked != 0 || first.Deleted != 1 || first.Failed != 1 {
		t.Fatalf("首轮结果 = %+v", first)
	}
	// 未确认资产没有媒体信息，删除失败后仍是已过期的 pending 行，下一轮会被再次选中。
	if status := assetStatus(t, env, stuckID); status != "pending" {
		t.Fatalf("失败对象必须保持可重试状态，实际 %s", status)
	}

	// 故障恢复后重试成功。
	delete(storage.failures, "cleanup/"+stuckID)
	second, err := service.Run(ctx)
	if err != nil || second.Deleted != 1 || second.Failed != 0 || second.Marked != 0 {
		t.Fatalf("重试结果 = %+v err=%v", second, err)
	}
	if status := assetStatus(t, env, stuckID); status != "" {
		t.Fatalf("重试成功后应移除行，实际状态 %q", status)
	}
}

// TestAssetCleanupKeepsOfflineArticleAssets 固化“下架不是删除”。
func TestAssetCleanupKeepsOfflineArticleAssets(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	now := fixedNow()
	seedAssetUsers(t, env, now)
	storage := &faultStorage{}
	service := newCleanupService(t, env, storage, now)
	ctx := context.Background()

	articleID, revisionID := seedArticleForAssets(t, env, 8301, 9301)
	const assetID = "5c000000-0000-0000-0000-000000000001"
	// 资产绑定到文章但已确认很久，仍不应被当成孤儿清理。
	seedCleanupAsset(t, env, assetID, "ready", &articleID, now.Add(-30*24*time.Hour))
	repository := postgres.NewArticleAssetRepository(env.pool)
	if err := repository.BindArticleAssets(ctx, binding(assetOwnerID, articleID, revisionID, []string{assetID}, now)); err != nil {
		t.Fatal(err)
	}
	// 管理员下架：文章离线但资产保留。
	if _, err := env.pool.Exec(ctx, `UPDATE velis.articles SET status='offline',offline_reason='admin',
offline_at=$2,updated_at=$2 WHERE id=$1`, articleID, now); err != nil {
		t.Fatal(err)
	}
	result, err := service.Run(ctx)
	if err != nil || result.Total() != 0 {
		t.Fatalf("下架文章的资产不得被清理: %+v err=%v", result, err)
	}
	if status := assetStatus(t, env, assetID); status != "ready" {
		t.Fatalf("下架后资产状态 = %s", status)
	}
}

// TestArticleDeleteMarksAssetsForDeletion 验证软删除在同一事务里标记资产待删除。
func TestArticleDeleteMarksAssetsForDeletion(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	now := fixedNow()
	seedAssetUsers(t, env, now)
	storage := &faultStorage{}
	service := newCleanupService(t, env, storage, now)
	ctx := context.Background()

	articleID, revisionID := seedArticleForAssets(t, env, 8302, 9302)
	const assetID = "5d000000-0000-0000-0000-000000000001"
	seedCleanupAsset(t, env, assetID, "ready", &articleID, now)
	repository := postgres.NewArticleAssetRepository(env.pool)
	if err := repository.BindArticleAssets(ctx, binding(assetOwnerID, articleID, revisionID, []string{assetID}, now)); err != nil {
		t.Fatal(err)
	}
	// 删除前资产由公开修订引用，匿名可读。
	if referenced, err := repository.IsPubliclyReferenced(ctx, assetID); err != nil || !referenced {
		t.Fatalf("删除前引用判定 = %t err=%v", referenced, err)
	}

	articles := postgres.NewArticleRepository(env.pool)
	deletedAt := now
	if _, err := articles.SetArticleState(ctx, articleID, 1, articleDomain.StatusDeleted, nil, nil, nil, &deletedAt, now); err != nil {
		t.Fatalf("软删除失败: %v", err)
	}
	// 数据库侧立即标记待删除并撤销匿名授权，对象删除交给清理任务。
	if status := assetStatus(t, env, assetID); status != "delete_pending" {
		t.Fatalf("软删除后资产状态 = %s", status)
	}
	if referenced, err := repository.IsPubliclyReferenced(ctx, assetID); err != nil || referenced {
		t.Fatalf("软删除后不得再授权匿名读取 = %t err=%v", referenced, err)
	}
	result, err := service.Run(ctx)
	if err != nil || result.Deleted != 1 {
		t.Fatalf("清理结果 = %+v err=%v", result, err)
	}
	if status := assetStatus(t, env, assetID); status != "deleted" {
		t.Fatalf("清理后状态 = %s", status)
	}
}

// TestAssetCleanupRejectsMissingDependencies 固化构造期校验。
func TestAssetCleanupRejectsMissingDependencies(t *testing.T) {
	if _, err := assetApp.NewCleanupService(assetApp.CleanupDeps{Batch: 10}); err == nil {
		t.Fatal("缺少依赖时必须报错")
	}
	if _, err := assetApp.NewCleanupService(assetApp.CleanupDeps{
		Repository: postgres.NewArticleAssetRepository(nil), Storage: &faultStorage{},
		Clock: &articleTestClock{now: fixedNow()}, Batch: 0,
	}); err == nil {
		t.Fatal("批大小为零必须报错")
	}
}
