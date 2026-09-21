package asset

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	assetDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/asset"
)

const (
	ownerID = "53000000-0000-0000-0000-000000000001"
	otherID = "53000000-0000-0000-0000-000000000002"
)

var testPolicy = Policy{
	Limits:         assetDomain.Limits{MaxFileBytes: 10 << 20, MaxWidth: 8192, MaxHeight: 8192, MaxPixels: 40000000},
	UserQuotaBytes: 1 << 30,
	PendingLimit:   20,
	UploadTTL:      15 * time.Minute,
	ProbeBytes:     1 << 20,
}

func testNow() time.Time { return time.Date(2026, 9, 21, 6, 0, 0, 0, time.UTC) }

// fakeRepository 是内存资产仓储，只实现应用层用到的方法。
type fakeRepository struct {
	assets map[string]assetDomain.Asset
	usage  assetDomain.Usage
	// lockCalls 记录所有者行锁是否在额度读取之前生效。
	lockCalls int
	saved     int
	usageErr  error
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{assets: make(map[string]assetDomain.Asset)}
}

func (f *fakeRepository) Create(_ context.Context, value assetDomain.Asset) error {
	f.assets[value.ID()] = value
	return nil
}

func (f *fakeRepository) Get(_ context.Context, assetID string) (assetDomain.Asset, error) {
	stored, ok := f.assets[assetID]
	if !ok {
		return assetDomain.Asset{}, assetDomain.ErrNotFound
	}
	return stored, nil
}

func (f *fakeRepository) Save(_ context.Context, value assetDomain.Asset) error {
	f.assets[value.ID()] = value
	f.saved++
	return nil
}

func (f *fakeRepository) LockOwner(context.Context, string) error {
	f.lockCalls++
	return nil
}

func (f *fakeRepository) Usage(context.Context, string) (assetDomain.Usage, error) {
	if f.usageErr != nil {
		return assetDomain.Usage{}, f.usageErr
	}
	usage := f.usage
	for _, stored := range f.assets {
		if stored.QuotaCountedAt() != nil && stored.Status() != assetDomain.StatusDeleted {
			usage.CountedBytes += stored.Media().SizeBytes
		}
		usage.PendingCount += 0
	}
	return usage, nil
}

func (f *fakeRepository) ListRevisionAssetIDs(context.Context, int64) ([]string, error) {
	return nil, nil
}

func (f *fakeRepository) IsPubliclyReferenced(context.Context, string) (bool, error) {
	return false, nil
}

// fakeStorage 是可编排的对象存储：用探测结果驱动确认流程的各条分支。
type fakeStorage struct {
	bytes      int64
	probe      ports.ImageProbe
	statErr    error
	probeErr   error
	presignErr error
	presigns   int
}

func (f *fakeStorage) EnsurePrivateBucket(context.Context) error { return nil }

func (f *fakeStorage) PresignUpload(_ context.Context, objectKey string, _ time.Duration) (ports.PresignedUpload, error) {
	if f.presignErr != nil {
		return ports.PresignedUpload{}, f.presignErr
	}
	f.presigns++
	return ports.PresignedUpload{URL: "https://storage.example/" + objectKey + "?X-Amz-Signature=abc",
		Method: "PUT", Headers: map[string]string{}}, nil
}

func (f *fakeStorage) StatObject(context.Context, string) (ports.ObjectInfo, error) {
	if f.statErr != nil {
		return ports.ObjectInfo{}, f.statErr
	}
	return ports.ObjectInfo{SizeBytes: f.bytes, ContentType: f.probe.ContentType, ETag: "etag-1"}, nil
}

func (f *fakeStorage) ProbeImage(context.Context, string, int64) (ports.ImageProbe, error) {
	if f.probeErr != nil {
		return ports.ImageProbe{}, f.probeErr
	}
	probe := f.probe
	probe.SizeBytes = f.bytes
	return probe, nil
}

func (f *fakeStorage) OpenObject(context.Context, string) (ports.ObjectStream, error) {
	return ports.ObjectStream{Body: io.NopCloser(nil)}, nil
}

func (f *fakeStorage) DeleteObject(context.Context, string) error { return nil }

// fakeIdempotency 是内存幂等记录：键相同且摘要相同即重放首次结果。
type fakeIdempotency struct {
	records map[string]idempotencyApp.Record
	calls   int
}

func newFakeIdempotency() *fakeIdempotency {
	return &fakeIdempotency{records: make(map[string]idempotencyApp.Record)}
}

func (f *fakeIdempotency) Begin(_ context.Context, identity idempotencyApp.Identity, digest [32]byte, now, expiresAt time.Time) (idempotencyApp.Record, bool, error) {
	key := identity.ActorUserID + "|" + identity.Operation + "|" + identity.Key
	if record, ok := f.records[key]; ok {
		if record.Digest != digest {
			return idempotencyApp.Record{}, false, idempotencyApp.ErrKeyReused
		}
		return record, false, nil
	}
	record := idempotencyApp.Record{Identity: identity, Digest: digest, Status: idempotencyApp.StatusPending,
		CreatedAt: now, ExpiresAt: expiresAt}
	f.records[key] = record
	return record, true, nil
}

func (f *fakeIdempotency) Succeed(_ context.Context, identity idempotencyApp.Identity, version int, result json.RawMessage, resourceType, resourceID string, completedAt time.Time) error {
	key := identity.ActorUserID + "|" + identity.Operation + "|" + identity.Key
	record := f.records[key]
	record.Status, record.ResultVersion, record.Result = idempotencyApp.StatusSucceeded, version, result
	record.CompletedAt = &completedAt
	f.records[key] = record
	return nil
}

type immediateTx struct{}

func (immediateTx) WithinTransaction(ctx context.Context, run func(context.Context) error) error {
	return run(ctx)
}

func newTestService(t *testing.T) (*Service, *fakeRepository, *fakeStorage) {
	t.Helper()
	repository := newFakeRepository()
	storage := &fakeStorage{}
	idempotency := idempotencyApp.NewService(newFakeIdempotency(), immediateTx{}, 24*time.Hour)
	clock := fixedClock{now: testNow()}
	return NewService(repository, storage, idempotency, clock, testPolicy), repository, storage
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func createCommand(key, contentType string, size int64) CreateCommand {
	return CreateCommand{OwnerUserID: ownerID, IdempotencyKey: key, ContentType: contentType, SizeBytes: size}
}

func TestCreateRejectsDeclaredLimits(t *testing.T) {
	service, repository, _ := newTestService(t)
	cases := map[string]struct {
		contentType string
		size        int64
	}{
		"类型不支持": {"image/gif", 1024},
		"大小为零":  {assetDomain.ContentTypePNG, 0},
		"超过单文件": {assetDomain.ContentTypePNG, testPolicy.Limits.MaxFileBytes + 1},
		"负数大小":  {assetDomain.ContentTypeJPEG, -1},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := service.Create(context.Background(), createCommand("key-"+name, testCase.contentType, testCase.size)); !errors.Is(err, assetDomain.ErrInvalidMedia) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if len(repository.assets) != 0 {
		t.Fatal("被拒绝的创建不得写入资产")
	}
}

func TestCreateEnforcesPendingLimitAndQuota(t *testing.T) {
	service, repository, _ := newTestService(t)
	repository.usage = assetDomain.Usage{PendingCount: testPolicy.PendingLimit}
	if _, _, err := service.Create(context.Background(), createCommand("pending-full", assetDomain.ContentTypePNG, 1024)); !errors.Is(err, ErrPendingLimitExceeded) {
		t.Fatalf("pending 上限 err = %v", err)
	}

	repository.usage = assetDomain.Usage{CountedBytes: testPolicy.UserQuotaBytes - 512}
	if _, _, err := service.Create(context.Background(), createCommand("quota-full", assetDomain.ContentTypePNG, 1024)); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("额度不足 err = %v", err)
	}
}

func TestCreateReturnsFixedObjectKeyAndPresignedUpload(t *testing.T) {
	service, _, _ := newTestService(t)
	upload, replayed, err := service.Create(context.Background(), createCommand("create-1", assetDomain.ContentTypePNG, 4096))
	if err != nil || replayed {
		t.Fatalf("upload=%+v replayed=%t err=%v", upload, replayed, err)
	}
	expectedKey, err := assetDomain.ObjectKey(ownerID, upload.Asset.ID())
	if err != nil {
		t.Fatal(err)
	}
	if upload.Asset.Status() != assetDomain.StatusPending || upload.Asset.ObjectKey() != expectedKey {
		t.Fatalf("资产 = %+v", upload.Asset)
	}
	if upload.Method != "PUT" || upload.URL == "" || !upload.ExpiresAt.Equal(testNow().Add(testPolicy.UploadTTL)) {
		t.Fatalf("直传信息 = %+v", upload)
	}
}

func TestCreateReplayReusesAssetAndIssuesFreshUpload(t *testing.T) {
	service, repository, storage := newTestService(t)
	first, _, err := service.Create(context.Background(), createCommand("same-key", assetDomain.ContentTypePNG, 4096))
	if err != nil {
		t.Fatal(err)
	}
	second, replayed, err := service.Create(context.Background(), createCommand("same-key", assetDomain.ContentTypePNG, 4096))
	if err != nil || !replayed {
		t.Fatalf("重放 replayed=%t err=%v", replayed, err)
	}
	// 同一幂等键必须复用同一资产，且不因重试产生第二条记录。
	if second.Asset.ID() != first.Asset.ID() || len(repository.assets) != 1 {
		t.Fatalf("重放资产 = %s 首次 = %s 记录数 = %d", second.Asset.ID(), first.Asset.ID(), len(repository.assets))
	}
	// 预签名地址有时效，重放要重新签发而不是复用。
	if storage.presigns != 2 {
		t.Fatalf("预签名次数 = %d，期望每次请求各签发一次", storage.presigns)
	}

	// 同键不同请求必须冲突。
	if _, _, err := service.Create(context.Background(), createCommand("same-key", assetDomain.ContentTypePNG, 8192)); !errors.Is(err, idempotencyApp.ErrKeyReused) {
		t.Fatalf("同键异请求 err = %v", err)
	}
}

func pendingAsset(t *testing.T, repository *fakeRepository) assetDomain.Asset {
	t.Helper()
	pending, err := assetDomain.NewPending("54000000-0000-0000-0000-000000000001", ownerID, testNow())
	if err != nil {
		t.Fatal(err)
	}
	repository.assets[pending.ID()] = pending
	return pending
}

func TestConfirmUsesProbedMetadataAndCountsQuotaOnce(t *testing.T) {
	service, repository, storage := newTestService(t)
	pending := pendingAsset(t, repository)
	storage.bytes = 2048
	storage.probe = ports.ImageProbe{ContentType: assetDomain.ContentTypePNG, Width: 800, Height: 600}

	confirmed, replayed, err := service.Confirm(context.Background(), ConfirmCommand{
		OwnerUserID: ownerID, AssetID: pending.ID(), IdempotencyKey: "confirm-1"})
	if err != nil || replayed {
		t.Fatalf("确认 replayed=%t err=%v", replayed, err)
	}
	media := confirmed.Media()
	if confirmed.Status() != assetDomain.StatusReady || media == nil ||
		media.ContentType != assetDomain.ContentTypePNG || media.SizeBytes != 2048 ||
		media.Width != 800 || media.Height != 600 || media.Checksum != "etag-1" {
		t.Fatalf("确认结果 = %+v", confirmed)
	}
	if confirmed.QuotaCountedAt() == nil || repository.lockCalls != 1 {
		t.Fatalf("额度计入 = %v 行锁次数 = %d", confirmed.QuotaCountedAt(), repository.lockCalls)
	}
	savedAfterFirst := repository.saved

	// 同键重放：返回同一结果，不再探测、不再计费。
	replayedAsset, replayed, err := service.Confirm(context.Background(), ConfirmCommand{
		OwnerUserID: ownerID, AssetID: pending.ID(), IdempotencyKey: "confirm-1"})
	if err != nil || !replayed || replayedAsset.ID() != confirmed.ID() {
		t.Fatalf("重放确认 replayed=%t err=%v", replayed, err)
	}
	if repository.saved != savedAfterFirst || repository.lockCalls != 1 {
		t.Fatalf("重放不得再次写入或计费: saved=%d lock=%d", repository.saved, repository.lockCalls)
	}
}

func TestConfirmEnforcesQuotaWithMeasuredSize(t *testing.T) {
	service, repository, storage := newTestService(t)
	pending := pendingAsset(t, repository)
	storage.bytes = 1 << 20
	storage.probe = ports.ImageProbe{ContentType: assetDomain.ContentTypeJPEG, Width: 100, Height: 100}
	// 声明阶段可能通过，实测大小才是计费依据。
	repository.usage = assetDomain.Usage{CountedBytes: testPolicy.UserQuotaBytes - 1024}

	if _, _, err := service.Confirm(context.Background(), ConfirmCommand{
		OwnerUserID: ownerID, AssetID: pending.ID(), IdempotencyKey: "confirm-quota"}); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("额度不足 err = %v", err)
	}
	stored, err := repository.Get(context.Background(), pending.ID())
	if err != nil || stored.Status() != assetDomain.StatusPending || stored.QuotaCountedAt() != nil {
		t.Fatalf("额度不足不得改变资产 = %+v err=%v", stored, err)
	}
}

func TestConfirmRejectsUnsupportedObjectsAndForeignOwnership(t *testing.T) {
	service, repository, storage := newTestService(t)
	pending := pendingAsset(t, repository)

	// 非所有者按不存在处理，不泄露私有元数据。
	if _, _, err := service.Confirm(context.Background(), ConfirmCommand{
		OwnerUserID: otherID, AssetID: pending.ID(), IdempotencyKey: "confirm-foreign"}); !errors.Is(err, assetDomain.ErrNotFound) {
		t.Fatalf("他人确认 err = %v", err)
	}

	// 每个拒绝原因都用独立幂等键，避免被幂等记录掩盖。
	cases := map[string]struct {
		key      string
		size     int64
		probe    ports.ImageProbe
		statErr  error
		probeErr error
		want     error
	}{
		"对象未上传": {key: "missing", size: 1024, statErr: ports.ErrObjectNotFound, want: ErrObjectMissing},
		"超过单文件": {key: "too-large", size: testPolicy.Limits.MaxFileBytes + 1, want: assetDomain.ErrInvalidMedia},
		"非图片":   {key: "not-image", size: 2048, probeErr: ports.ErrImageUnsupported, want: ports.ErrImageUnsupported},
		"像素超限":  {key: "pixels", size: 2048, probe: ports.ImageProbe{ContentType: assetDomain.ContentTypePNG, Width: 8000, Height: 8000}, want: assetDomain.ErrInvalidMedia},
		"存储故障":  {key: "storage-down", size: 2048, statErr: ports.ErrAssetStorageUnavailable, want: ports.ErrAssetStorageUnavailable},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			storage.bytes, storage.probe = testCase.size, testCase.probe
			storage.statErr, storage.probeErr = testCase.statErr, testCase.probeErr
			_, _, err := service.Confirm(context.Background(), ConfirmCommand{
				OwnerUserID: ownerID, AssetID: pending.ID(), IdempotencyKey: "confirm-" + testCase.key})
			if !errors.Is(err, testCase.want) {
				t.Fatalf("err = %v，期望 %v", err, testCase.want)
			}
			stored, getErr := repository.Get(context.Background(), pending.ID())
			if getErr != nil || stored.Status() != assetDomain.StatusPending {
				t.Fatalf("失败的确认不得改变资产 = %+v", stored)
			}
		})
	}
	if !IsObjectMissing(ErrObjectMissing) {
		t.Fatal("IsObjectMissing 应识别未上传错误")
	}
	if IsObjectMissing(ports.ErrAssetStorageUnavailable) {
		t.Fatal("存储故障不得被当成对象未上传")
	}
}
