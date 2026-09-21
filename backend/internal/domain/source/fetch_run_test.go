package source

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func runNow() time.Time { return time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC) }

func scheduledRun(t *testing.T) FetchRun {
	t.Helper()
	run, err := NewFetchRun("6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c11", 7, TriggerScheduled, nil, 3, runNow())
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestNewFetchRunValidation(t *testing.T) {
	actor := "51000000-0000-0000-0000-000000000001"
	manual, err := NewFetchRun("6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c11", 7, TriggerManual, &actor, 1, runNow())
	if err != nil || manual.Status != RunRunning || manual.ActorUserID == nil {
		t.Fatalf("手动运行 = %+v err=%v", manual, err)
	}
	if _, err := NewFetchRun("", 7, TriggerScheduled, nil, 1, runNow()); !errors.Is(err, ErrInvalidRun) {
		t.Fatalf("空 ID err = %v", err)
	}
	if _, err := NewFetchRun("run", 0, TriggerScheduled, nil, 1, runNow()); !errors.Is(err, ErrInvalidRun) {
		t.Fatalf("来源 ID 为零 err = %v", err)
	}
	// generation 是 fencing 依据：为零说明调用方没有真正持有租约。
	if _, err := NewFetchRun("run", 7, TriggerScheduled, nil, 0, runNow()); !errors.Is(err, ErrInvalidRun) {
		t.Fatalf("generation 为零 err = %v", err)
	}
	// 手动触发必须带操作者，定时触发不得伪造操作者。
	if _, err := NewFetchRun("run", 7, TriggerManual, nil, 1, runNow()); !errors.Is(err, ErrInvalidRun) {
		t.Fatalf("手动缺少操作者 err = %v", err)
	}
	if _, err := NewFetchRun("run", 7, TriggerScheduled, &actor, 1, runNow()); !errors.Is(err, ErrInvalidRun) {
		t.Fatalf("定时携带操作者 err = %v", err)
	}
}

func TestFetchRunSucceedRecordsNotModifiedAndStats(t *testing.T) {
	run := scheduledRun(t)
	stats := FetchRunStats{NotModified: true, Inserted: 0, Updated: 0, Unchanged: 3, Skipped: 1}
	if err := run.Succeed(stats, runNow().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if run.Status != RunSucceeded || !run.NotModified || run.Unchanged != 3 || run.Skipped != 1 {
		t.Fatalf("成功结果 = %+v", run)
	}
	if run.CompletedAt == nil || run.ErrorCode != nil {
		t.Fatalf("终态 = %+v", run)
	}
	// 终态不可再转换。
	if err := run.Fail("FETCH_FAILED", runNow().Add(2*time.Second)); !errors.Is(err, ErrInvalidRunState) {
		t.Fatalf("终态重复结束 err = %v", err)
	}
	if err := run.Succeed(FetchRunStats{}, runNow().Add(2*time.Second)); !errors.Is(err, ErrInvalidRunState) {
		t.Fatalf("终态重复成功 err = %v", err)
	}
}

func TestFetchRunFailAndAbortKeepErrorCodeControlled(t *testing.T) {
	failed := scheduledRun(t)
	if err := failed.Fail("  ", runNow().Add(time.Second)); !errors.Is(err, ErrInvalidRun) {
		t.Fatalf("空错误码 err = %v", err)
	}
	if err := failed.Fail(strings.Repeat("x", 65), runNow().Add(time.Second)); !errors.Is(err, ErrInvalidRun) {
		t.Fatalf("超长错误码 err = %v", err)
	}
	if err := failed.Fail("SSRF_BLOCKED", runNow().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if failed.Status != RunFailed || failed.ErrorCode == nil || *failed.ErrorCode != "SSRF_BLOCKED" {
		t.Fatalf("失败结果 = %+v", failed)
	}

	// 中止用于收敛崩溃遗留：不得携带错误码，否则会被当成受控失败分类。
	aborted := scheduledRun(t)
	if err := aborted.Abort(runNow().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if aborted.Status != RunAborted || aborted.ErrorCode != nil || aborted.CompletedAt == nil {
		t.Fatalf("中止结果 = %+v", aborted)
	}
}

func TestFetchRunRejectsCompletedBeforeStart(t *testing.T) {
	run := scheduledRun(t)
	if err := run.Succeed(FetchRunStats{}, runNow().Add(-time.Second)); !errors.Is(err, ErrInvalidRun) {
		t.Fatalf("完成早于开始 err = %v", err)
	}
	if run.Status != RunRunning || run.CompletedAt != nil {
		t.Fatalf("被拒绝的写入不得改变状态: %+v", run)
	}
}

func TestNormalizeFetchRunLimit(t *testing.T) {
	if limit, err := NormalizeFetchRunLimit(0); err != nil || limit != DefaultFetchRunPageSize {
		t.Fatalf("默认分页 = %d err=%v", limit, err)
	}
	if limit, err := NormalizeFetchRunLimit(MaxFetchRunPageSize); err != nil || limit != MaxFetchRunPageSize {
		t.Fatalf("上限分页 = %d err=%v", limit, err)
	}
	for _, limit := range []int{-1, MaxFetchRunPageSize + 1} {
		if _, err := NormalizeFetchRunLimit(limit); !errors.Is(err, ErrInvalidPageSize) {
			t.Fatalf("分页 %d err = %v", limit, err)
		}
	}
}
