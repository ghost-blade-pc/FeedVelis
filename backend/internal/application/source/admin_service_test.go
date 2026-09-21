package source

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
)

const adminActor = "51000000-0000-0000-0000-000000000001"

// adminRepositoryFake 是无法访问数据库时对 Source 仓储的最小实现。
type adminRepositoryFake struct {
	source       sourceDomain.Source
	inserted     bool
	addCalls     int
	mutations    int
	versionError error
	lastInterval time.Duration
}

func (f *adminRepositoryFake) Add(_ context.Context, rawURL, normalizedURL, title string, interval time.Duration, now time.Time) (sourceDomain.Source, bool, error) {
	f.addCalls++
	f.lastInterval = interval
	source := f.source
	if source.ID == 0 {
		source = sourceDomain.Source{ID: 9, FeedURL: rawURL, NormalizedFeedURL: normalizedURL, Title: title,
			Status: sourceDomain.StatusActive, FetchInterval: interval, LockVersion: 1, NextFetchAt: now}
	}
	return source, f.inserted, nil
}

func (f *adminRepositoryFake) List(context.Context) ([]sourceDomain.Source, error) {
	return []sourceDomain.Source{f.source}, nil
}

func (f *adminRepositoryFake) Get(context.Context, int64) (sourceDomain.Source, error) {
	return f.source, nil
}

func (f *adminRepositoryFake) ClaimByID(context.Context, int64, string, time.Time, time.Time) (sourceDomain.Source, error) {
	return f.source, nil
}

func (f *adminRepositoryFake) Pause(_ context.Context, _, version int64, now time.Time) (sourceDomain.Source, error) {
	f.mutations++
	updated := f.source
	updated.Status, updated.LockVersion, updated.UpdatedAt = sourceDomain.StatusPaused, version+1, now
	f.source = updated
	return updated, nil
}

func (f *adminRepositoryFake) Resume(_ context.Context, _, version int64, now time.Time) (sourceDomain.Source, error) {
	f.mutations++
	updated := f.source
	updated.Status, updated.LockVersion, updated.UpdatedAt = sourceDomain.StatusActive, version+1, now
	f.source = updated
	return updated, nil
}

func (f *adminRepositoryFake) SetFetchInterval(_ context.Context, _, version int64, interval time.Duration, now time.Time) (sourceDomain.Source, error) {
	f.mutations++
	f.lastInterval = interval
	updated := f.source
	updated.FetchInterval, updated.LockVersion, updated.UpdatedAt = interval, version+1, now
	f.source = updated
	return updated, nil
}

func (*adminRepositoryFake) ClaimDue(context.Context, string, time.Time, time.Time, int) ([]sourceDomain.Source, error) {
	return nil, nil
}
func (*adminRepositoryFake) MarkNotModified(context.Context, int64, sourceDomain.Lease, *string, *string, time.Time, time.Time) error {
	return nil
}
func (*adminRepositoryFake) MarkSuccess(context.Context, int64, sourceDomain.Lease, sourceDomain.Metadata, time.Time, time.Time) error {
	return nil
}
func (*adminRepositoryFake) MarkFailure(context.Context, int64, sourceDomain.Lease, sourceDomain.FailureUpdate) error {
	return nil
}

// adminRunRepositoryFake 提供运行历史的可控状态。
type adminRunRepositoryFake struct {
	started []sourceDomain.FetchRun
	running *sourceDomain.FetchRun
	history []sourceDomain.FetchRun
}

func (f *adminRunRepositoryFake) Start(_ context.Context, run sourceDomain.FetchRun) error {
	f.started = append(f.started, run)
	return nil
}

func (f *adminRunRepositoryFake) Complete(context.Context, sourceDomain.FetchRun) error { return nil }

func (f *adminRunRepositoryFake) AbortStale(context.Context, int64, int64, time.Time) (int64, error) {
	return 0, nil
}

func (f *adminRunRepositoryFake) ListBySource(context.Context, int64, *sourceDomain.FetchRunCursor, int) ([]sourceDomain.FetchRun, error) {
	return f.history, nil
}

func (f *adminRunRepositoryFake) CurrentRunning(context.Context, int64) (sourceDomain.FetchRun, error) {
	if f.running == nil {
		return sourceDomain.FetchRun{}, sourceDomain.ErrNotFound
	}
	return *f.running, nil
}

type memoryIdempotency struct {
	records map[string]idempotencyApp.Record
}

func newMemoryIdempotency() *memoryIdempotency {
	return &memoryIdempotency{records: map[string]idempotencyApp.Record{}}
}

func (m *memoryIdempotency) key(identity idempotencyApp.Identity) string {
	return identity.ActorUserID + "|" + identity.Operation + "|" + identity.Key
}

func (m *memoryIdempotency) Begin(_ context.Context, identity idempotencyApp.Identity, digest [32]byte, now, expiresAt time.Time) (idempotencyApp.Record, bool, error) {
	key := m.key(identity)
	if record, ok := m.records[key]; ok {
		if record.Digest != digest {
			return idempotencyApp.Record{}, false, idempotencyApp.ErrKeyReused
		}
		return record, false, nil
	}
	record := idempotencyApp.Record{Identity: identity, Digest: digest, Status: idempotencyApp.StatusPending,
		CreatedAt: now, ExpiresAt: expiresAt}
	m.records[key] = record
	return record, true, nil
}

func (m *memoryIdempotency) Succeed(_ context.Context, identity idempotencyApp.Identity, version int, result json.RawMessage, resourceType, resourceID string, completedAt time.Time) error {
	key := m.key(identity)
	record := m.records[key]
	record.Status, record.ResultVersion, record.Result = idempotencyApp.StatusSucceeded, version, result
	record.CompletedAt = &completedAt
	m.records[key] = record
	return nil
}

type adminClock struct{ now time.Time }

func (c adminClock) Now() time.Time { return c.now }

func newAdminService(t *testing.T, repository *adminRepositoryFake, runs *adminRunRepositoryFake) *AdminService {
	t.Helper()
	clock := adminClock{now: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)}
	idempotency := idempotencyApp.NewService(newMemoryIdempotency(), transactionManagerFake{}, 24*time.Hour)
	articles := newArticleServiceForAdmin(t, clock)
	fetch := NewService(repository, runs, fetcherFake{}, &parserFake{}, articles, clock, transactionManagerFake{}, nil)
	return NewAdminService(repository, runs, fetch, idempotency, clock)
}

func TestAdminCreateUsesDefaultIntervalAndReplaysSameKey(t *testing.T) {
	repository := &adminRepositoryFake{inserted: true}
	service := newAdminService(t, repository, &adminRunRepositoryFake{})
	command := CreateCommand{ActorUserID: adminActor, IdempotencyKey: "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c11",
		FeedURL: "https://new.example/feed"}

	result, replayed, err := service.Create(context.Background(), command)
	if err != nil || replayed || !result.Created || result.Source.ID != 9 {
		t.Fatalf("首次创建 = %+v replayed=%t err=%v", result, replayed, err)
	}
	if repository.lastInterval != sourceDomain.DefaultFetchInterval {
		t.Fatalf("默认周期 = %v", repository.lastInterval)
	}
	// 同键重放：不重复创建。
	_, replayed, err = service.Create(context.Background(), command)
	if err != nil || !replayed || repository.addCalls != 1 {
		t.Fatalf("重放 replayed=%t addCalls=%d err=%v", replayed, repository.addCalls, err)
	}
	// 同键异请求冲突。
	conflicting := command
	conflicting.FeedURL = "https://other.example/feed"
	if _, _, err := service.Create(context.Background(), conflicting); !errors.Is(err, idempotencyApp.ErrKeyReused) {
		t.Fatalf("同键异请求 err = %v", err)
	}
	// 非法周期在进入用例前被拒。
	if _, _, err := service.Create(context.Background(), CreateCommand{ActorUserID: adminActor,
		IdempotencyKey: "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c12", FeedURL: "https://fast.example/feed",
		FetchInterval: time.Minute}); !errors.Is(err, sourceDomain.ErrInvalidInterval) {
		t.Fatalf("非法周期 err = %v", err)
	}
}

func TestAdminCreateReportsExistingSource(t *testing.T) {
	repository := &adminRepositoryFake{inserted: false, source: sourceDomain.Source{ID: 4, LockVersion: 3,
		Status: sourceDomain.StatusActive, NormalizedFeedURL: "https://dup.example/feed"}}
	service := newAdminService(t, repository, &adminRunRepositoryFake{})
	result, _, err := service.Create(context.Background(), CreateCommand{ActorUserID: adminActor,
		IdempotencyKey: "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c13", FeedURL: "https://dup.example/feed"})
	if err != nil || result.Created || result.Source.ID != 4 {
		t.Fatalf("重复 URL 结果 = %+v err=%v", result, err)
	}
}

func TestAdminMutationsRequireCurrentVersion(t *testing.T) {
	repository := &adminRepositoryFake{source: sourceDomain.Source{ID: 4, LockVersion: 3,
		Status: sourceDomain.StatusActive, FetchInterval: sourceDomain.DefaultFetchInterval}}
	service := newAdminService(t, repository, &adminRunRepositoryFake{})
	ctx := context.Background()
	base := SourceCommand{ActorUserID: adminActor, SourceID: 4, ExpectedVersion: 3}

	// 旧版本在进入写入前就被拒绝。
	stale := base
	stale.IdempotencyKey, stale.ExpectedVersion = "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c21", 2
	if _, _, err := service.Pause(ctx, stale); !errors.Is(err, sourceDomain.ErrVersionConflict) {
		t.Fatalf("旧版本暂停 err = %v", err)
	}
	if repository.mutations != 0 {
		t.Fatal("版本冲突不得写入")
	}

	pause := base
	pause.IdempotencyKey = "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c22"
	paused, replayed, err := service.Pause(ctx, pause)
	if err != nil || replayed || paused.Status != sourceDomain.StatusPaused || paused.LockVersion != 4 {
		t.Fatalf("暂停 = %+v replayed=%t err=%v", paused, replayed, err)
	}
	// 同键重放不再次写入。
	if _, replayed, err := service.Pause(ctx, pause); err != nil || !replayed || repository.mutations != 1 {
		t.Fatalf("暂停重放 replayed=%t mutations=%d err=%v", replayed, repository.mutations, err)
	}
	// 已暂停时再暂停保持幂等：不递增版本。
	again := SourceCommand{ActorUserID: adminActor, SourceID: 4, ExpectedVersion: 4,
		IdempotencyKey: "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c23"}
	if repeated, _, err := service.Pause(ctx, again); err != nil || repeated.LockVersion != 4 {
		t.Fatalf("重复暂停 = %+v err=%v", repeated, err)
	}

	resume := SourceCommand{ActorUserID: adminActor, SourceID: 4, ExpectedVersion: 4,
		IdempotencyKey: "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c24"}
	if resumed, _, err := service.Resume(ctx, resume); err != nil || resumed.Status != sourceDomain.StatusActive {
		t.Fatalf("恢复 = %+v err=%v", resumed, err)
	}

	interval := SourceCommand{ActorUserID: adminActor, SourceID: 4, ExpectedVersion: 5, Interval: 2 * time.Hour,
		IdempotencyKey: "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c25"}
	updated, _, err := service.SetInterval(ctx, interval)
	if err != nil || updated.FetchInterval != 2*time.Hour || updated.FeedURL != repository.source.FeedURL {
		t.Fatalf("修改周期 = %+v err=%v", updated, err)
	}
	// 非法周期在入口被拒。
	interval.Interval, interval.IdempotencyKey = time.Second, "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c26"
	if _, _, err := service.SetInterval(ctx, interval); !errors.Is(err, sourceDomain.ErrInvalidInterval) {
		t.Fatalf("非法周期 err = %v", err)
	}
}

func TestAdminFetchNowReturnsRunningRunForPendingKey(t *testing.T) {
	repository := &adminRepositoryFake{source: sourceDomain.Source{ID: 4, LockVersion: 1,
		Status: sourceDomain.StatusActive}}
	actor := adminActor
	running := sourceDomain.Restore("6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c31", 4, sourceDomain.TriggerManual,
		&actor, sourceDomain.RunRunning, 1, sourceDomain.FetchRunStats{}, nil,
		time.Date(2026, 9, 21, 9, 59, 0, 0, time.UTC), nil, time.Date(2026, 9, 21, 9, 59, 0, 0, time.UTC))
	runs := &adminRunRepositoryFake{running: &running}
	service := newAdminService(t, repository, runs)

	// 先占位但不结算，模拟首次执行仍在进行。
	identity := idempotencyApp.Identity{ActorUserID: adminActor, Operation: "source.fetch",
		Key: "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c32"}
	payload := struct {
		SourceID int64 `json:"source_id"`
		Force    bool  `json:"force"`
	}{4, false}
	if _, err := service.idempotency.Reserve(context.Background(), idempotencyApp.Command{
		Identity: identity, Payload: payload, Now: time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}

	result, err := service.FetchNow(context.Background(), FetchCommand{ActorUserID: adminActor,
		IdempotencyKey: identity.Key, SourceID: 4})
	if err != nil || !result.Running || result.Run.ID != running.ID {
		t.Fatalf("运行中的同键重试 = %+v err=%v", result, err)
	}
	if len(runs.started) != 0 {
		t.Fatal("运行中的同键重试不得发起第二次抓取")
	}
}

func TestHistoryCursorIsOpaqueAndRoundTrips(t *testing.T) {
	runs := &adminRunRepositoryFake{history: []sourceDomain.FetchRun{
		sourceDomain.Restore("6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c41", 4, sourceDomain.TriggerScheduled, nil,
			sourceDomain.RunSucceeded, 1, sourceDomain.FetchRunStats{}, nil,
			time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC), nil, time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)),
	}}
	service := newAdminService(t, &adminRepositoryFake{}, runs)
	page, err := service.History(context.Background(), HistoryCommand{SourceID: 4, Limit: 0})
	if err != nil || len(page.Items) != 1 || page.NextCursor != nil {
		t.Fatalf("历史 = %+v err=%v", page, err)
	}
	// 游标解码失败必须是可识别的参数错误。
	if _, err := service.History(context.Background(), HistoryCommand{SourceID: 4, Cursor: "not-a-cursor"}); !errors.Is(err, sourceDomain.ErrInvalidRunCursor) {
		t.Fatalf("非法游标 err = %v", err)
	}
	if _, err := service.History(context.Background(), HistoryCommand{SourceID: 4, Limit: 101}); !errors.Is(err, sourceDomain.ErrInvalidPageSize) {
		t.Fatalf("越界分页 err = %v", err)
	}
}

// newArticleServiceForAdmin 构造用于抓取入库的文章服务。
func newArticleServiceForAdmin(t *testing.T, clock ports.Clock) *articleApp.Service {
	t.Helper()
	return articleApp.NewService(articleRepositoryFake{}, sanitizerFake{}, clock)
}

var _ ports.Clock = adminClock{}
