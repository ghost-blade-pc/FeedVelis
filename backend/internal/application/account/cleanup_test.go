package account

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeCleanup 按分类返回预置的删除批次，并记录调用时的保留期截止时间与批大小。
type fakeCleanup struct {
	batches map[string][]int64
	calls   map[string]int
	cutoffs map[string]time.Time
	failOn  string
}

func newFakeCleanup(batches map[string][]int64) *fakeCleanup {
	return &fakeCleanup{batches: batches, calls: map[string]int{}, cutoffs: map[string]time.Time{}}
}

func (f *fakeCleanup) remove(kind string, cutoff time.Time, limit int) (int64, error) {
	f.calls[kind]++
	f.cutoffs[kind] = cutoff
	if f.failOn == kind {
		return 0, errors.New("清理失败")
	}
	series := f.batches[kind]
	index := f.calls[kind] - 1
	if index >= len(series) {
		return 0, nil
	}
	return series[index], nil
}

func (f *fakeCleanup) DeleteExpiredSessions(_ context.Context, cutoff time.Time, limit int) (int64, error) {
	return f.remove("sessions", cutoff, limit)
}

func (f *fakeCleanup) DeleteExpiredRefreshTokens(_ context.Context, cutoff time.Time, limit int) (int64, error) {
	return f.remove("tokens", cutoff, limit)
}

func (f *fakeCleanup) DeleteStaleFailures(_ context.Context, cutoff time.Time, limit int) (int64, error) {
	return f.remove("failures", cutoff, limit)
}

func (f *fakeCleanup) DeleteExpiredBlocks(_ context.Context, cutoff time.Time, limit int) (int64, error) {
	return f.remove("blocks", cutoff, limit)
}

func (f *fakeCleanup) DeleteExpiredIdempotency(_ context.Context, cutoff time.Time, limit int) (int64, error) {
	return f.remove("idempotency", cutoff, limit)
}

func newCleanupService(t *testing.T, repository *fakeCleanup, batch int) (*CleanupService, time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	service, err := NewCleanupService(CleanupDeps{Repository: repository, Clock: &fakeClock{now: now}, Batch: batch})
	if err != nil {
		t.Fatalf("装配清理用例失败: %v", err)
	}
	return service, now
}

func TestCleanupAppliesRetentionCutoffs(t *testing.T) {
	repository := newFakeCleanup(map[string][]int64{})
	service, now := newCleanupService(t, repository, 100)

	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if result.Total() != 0 {
		t.Fatalf("空库不应删除任何行，实际 %d", result.Total())
	}
	cases := map[string]time.Duration{
		"sessions":    SessionRetention,
		"tokens":      SessionRetention,
		"failures":    FailureRetention,
		"blocks":      BlockRetention,
		"idempotency": 0,
	}
	for kind, retention := range cases {
		want := now.Add(-retention)
		if got := repository.cutoffs[kind]; !got.Equal(want) {
			t.Fatalf("%s 的保留期截止时间应为 %s，实际 %s", kind, want, got)
		}
	}
}

func TestCleanupDrainsUntilBatchNotFull(t *testing.T) {
	// 会话两批（100+40）后不足一批；令牌恰好一批，需再探一次才确认清空；失败与限制为空。
	repository := newFakeCleanup(map[string][]int64{
		"sessions": {100, 40},
		"tokens":   {100},
	})
	service, _ := newCleanupService(t, repository, 100)

	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if result.Sessions != 140 || result.Tokens != 100 {
		t.Fatalf("删除数量不符：sessions=%d tokens=%d", result.Sessions, result.Tokens)
	}
	if repository.calls["sessions"] != 2 {
		t.Fatalf("满批后应再取一批，实际调用 %d 次", repository.calls["sessions"])
	}
	// 满批表示可能还有，需再取一次空批才停止。
	if repository.calls["tokens"] != 2 {
		t.Fatalf("满批后应探到空批为止，实际调用 %d 次", repository.calls["tokens"])
	}
}

func TestCleanupStopsAtBatchCap(t *testing.T) {
	// 每批都满：应停在单轮上限，不无限循环。
	series := make([]int64, maxCleanupBatchesPerRun+5)
	for i := range series {
		series[i] = 10
	}
	repository := newFakeCleanup(map[string][]int64{"sessions": series})
	service, _ := newCleanupService(t, repository, 10)

	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if repository.calls["sessions"] != maxCleanupBatchesPerRun {
		t.Fatalf("应停止在单轮批次上限 %d，实际 %d", maxCleanupBatchesPerRun, repository.calls["sessions"])
	}
	if result.Sessions != int64(maxCleanupBatchesPerRun*10) {
		t.Fatalf("删除数量应为上限批次的合计，实际 %d", result.Sessions)
	}
}

func TestCleanupReportsPartialResultOnFailure(t *testing.T) {
	repository := newFakeCleanup(map[string][]int64{"failures": {100, 100}})
	repository.failOn = "failures"
	service, _ := newCleanupService(t, repository, 100)

	result, err := service.Run(context.Background())
	if err == nil {
		t.Fatal("清理失败时应返回错误")
	}
	if result.Failures != 0 {
		t.Fatalf("失败分类不应计为已删除，实际 %d", result.Failures)
	}
}

func TestCleanupIdempotencyBatchCapAndRetry(t *testing.T) {
	series := make([]int64, maxCleanupBatchesPerRun+2)
	for index := range series {
		series[index] = 5
	}
	repository := newFakeCleanup(map[string][]int64{"idempotency": series})
	service, _ := newCleanupService(t, repository, 5)
	result, err := service.Run(context.Background())
	if err != nil || result.Idempotency != int64(maxCleanupBatchesPerRun*5) || repository.calls["idempotency"] != maxCleanupBatchesPerRun {
		t.Fatalf("幂等清理批量上限 result=%+v calls=%d err=%v", result, repository.calls["idempotency"], err)
	}

	repository = newFakeCleanup(map[string][]int64{"idempotency": {3}})
	repository.failOn = "idempotency"
	service, _ = newCleanupService(t, repository, 5)
	if _, err := service.Run(context.Background()); err == nil {
		t.Fatal("幂等清理失败应保留错误供下一轮重试")
	}
	repository.failOn = ""
	result, err = service.Run(context.Background())
	if err != nil || result.Idempotency != 0 {
		// fake 的首次失败已经消耗一次调用序号；重试证明调度用例没有永久停用该分类。
		t.Fatalf("失败后重试 result=%+v err=%v", result, err)
	}
}

func TestCleanupRejectsInvalidDeps(t *testing.T) {
	cases := map[string]CleanupDeps{
		"缺仓储":   {Clock: &fakeClock{}, Batch: 10},
		"缺时钟":   {Repository: newFakeCleanup(nil), Batch: 10},
		"批大小为零": {Repository: newFakeCleanup(nil), Clock: &fakeClock{}, Batch: 0},
	}
	for name, deps := range cases {
		if _, err := NewCleanupService(deps); err == nil {
			t.Fatalf("%s 应被拒绝", name)
		}
	}
}
