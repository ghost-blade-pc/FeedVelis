package searchprojection

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func testNow() time.Time { return time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC) }

func text(value string) *string { return &value }

func mustTarget(t *testing.T, action Action, lockVersion, revisionID int64, generation, embedding *string) Target {
	t.Helper()
	target, err := NewTarget(action, lockVersion, revisionID, generation, embedding)
	if err != nil {
		t.Fatalf("构造目标: %v", err)
	}
	return target
}

func TestNewTargetNormalizesIdentifiers(t *testing.T) {
	blank := mustTarget(t, ActionUpsert, 3, 30, text("  "), text(""))
	if blank.GenerationResultID != nil || blank.EmbeddingResultID != nil {
		t.Fatalf("空白标识应规范化为 nil: %+v", blank)
	}
	spaced := mustTarget(t, ActionUpsert, 3, 30, text(" g-1 "), nil)
	if spaced.GenerationResultID == nil || *spaced.GenerationResultID != "g-1" {
		t.Fatalf("标识未去除首尾空白: %+v", spaced.GenerationResultID)
	}
	if !spaced.Equal(mustTarget(t, ActionUpsert, 3, 30, text("g-1"), nil)) {
		t.Fatal("规范化后的相同目标必须相等")
	}
}

func TestNewTargetRejectsIncompleteIdentity(t *testing.T) {
	cases := map[string]struct {
		action      Action
		lockVersion int64
		revisionID  int64
		generation  *string
	}{
		"未知动作":               {action: Action("index"), lockVersion: 1, revisionID: 1},
		"缺少文章版本":             {action: ActionUpsert, lockVersion: 0, revisionID: 1},
		"缺少修订":               {action: ActionUpsert, lockVersion: 1, revisionID: 0},
		"tombstone 携带 AI 结果": {action: ActionTombstone, lockVersion: 1, revisionID: 1, generation: text("g-1")},
	}
	for name, item := range cases {
		if _, err := NewTarget(item.action, item.lockVersion, item.revisionID, item.generation, nil); !errors.Is(err, ErrInvalidTarget) {
			t.Fatalf("%s: 期望 ErrInvalidTarget，实际 %v", name, err)
		}
	}
}

func TestAdvanceCreatesInitialJobWithGenerationOne(t *testing.T) {
	target := mustTarget(t, ActionUpsert, 1, 10, nil, nil)
	job, changed, err := Advance(7, nil, target, 42, testNow())
	if err != nil || !changed {
		t.Fatalf("首次推进必须建立槽位: changed=%t err=%v", changed, err)
	}
	if job.Generation != 1 || job.ChangeSeq != 42 || job.Status != StatusPending || job.Attempt != 0 {
		t.Fatalf("初始槽位字段错误: %+v", job)
	}
	if job.Lease != nil || job.CompletedAt != nil || job.NextAttemptAt != testNow() {
		t.Fatalf("初始槽位不应携带租约或完成时间: %+v", job)
	}
}

func TestAdvanceIsNoopForIdenticalTarget(t *testing.T) {
	target := mustTarget(t, ActionUpsert, 4, 40, text("g-1"), text("e-1"))
	now := testNow()
	current := Job{
		ArticleID: 7, Target: target, Generation: 3, ChangeSeq: 100, Status: StatusSucceeded,
		Attempt: 2, NextAttemptAt: now.Add(-time.Minute), CompletedAt: &now, CreatedAt: now.Add(-time.Hour),
	}
	// 用语义相同但书写不同的标识重复推进，仍必须是不做任何修改的 noop。
	same := mustTarget(t, ActionUpsert, 4, 40, text(" g-1"), text("e-1 "))
	job, changed, err := Advance(7, &current, same, 999, now)
	if err != nil || changed {
		t.Fatalf("相同目标必须为 noop: changed=%t err=%v", changed, err)
	}
	if job.Generation != 3 || job.ChangeSeq != 100 || job.Status != StatusSucceeded || job.CompletedAt == nil {
		t.Fatalf("noop 不得修改槽位: %+v", job)
	}
	if job.Attempt != 2 {
		t.Fatalf("noop 不得重置尝试次数: %d", job.Attempt)
	}
}

func TestAdvanceIncrementsGenerationOnEveryTargetChange(t *testing.T) {
	base := mustTarget(t, ActionUpsert, 4, 40, text("g-1"), text("e-1"))
	current := Job{ArticleID: 7, Target: base, Generation: 5, ChangeSeq: 100, Status: StatusSucceeded, Attempt: 3, CreatedAt: testNow().Add(-time.Hour)}
	completed := testNow().Add(-time.Minute)
	current.CompletedAt = &completed

	changes := map[string]Target{
		"文章版本变化":        mustTarget(t, ActionUpsert, 5, 40, text("g-1"), text("e-1")),
		"修订变化":          mustTarget(t, ActionUpsert, 4, 41, text("g-1"), text("e-1")),
		"generation 变化": mustTarget(t, ActionUpsert, 4, 40, text("g-2"), text("e-1")),
		"embedding 变化":  mustTarget(t, ActionUpsert, 4, 40, text("g-1"), text("e-2")),
		"向量被清空":         mustTarget(t, ActionUpsert, 4, 40, text("g-1"), nil),
		"下架为 tombstone": mustTarget(t, ActionTombstone, 5, 40, nil, nil),
	}
	for name, target := range changes {
		job, changed, err := Advance(7, &current, target, 101, testNow())
		if err != nil || !changed {
			t.Fatalf("%s: 目标变化必须推进槽位: changed=%t err=%v", name, changed, err)
		}
		if job.Generation != 6 || job.ChangeSeq != 101 {
			t.Fatalf("%s: generation 与 change sequence 必须单调递增: %+v", name, job)
		}
		if job.Status != StatusPending || job.Attempt != 0 || job.Lease != nil || job.CompletedAt != nil {
			t.Fatalf("%s: 新目标必须回到待处理并清除租约与完成时间: %+v", name, job)
		}
		if !job.Target.Equal(target) {
			t.Fatalf("%s: 槽位未保存新目标", name)
		}
		if !job.CreatedAt.Equal(current.CreatedAt) {
			t.Fatalf("%s: 推进不得丢失创建时间", name)
		}
	}
}

func TestAdvanceRejectsInvalidIdentity(t *testing.T) {
	target := mustTarget(t, ActionUpsert, 1, 10, nil, nil)
	if _, _, err := Advance(0, nil, target, 1, testNow()); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("文章 ID 无效应被拒绝: %v", err)
	}
	if _, _, err := Advance(7, nil, target, 0, testNow()); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("change sequence 无效应被拒绝: %v", err)
	}
}

func deliveriesOf(t *testing.T, generation int64, statuses ...Status) []Delivery {
	t.Helper()
	items := make([]Delivery, 0, len(statuses))
	for index, status := range statuses {
		items = append(items, Delivery{
			ArticleID: 7, PhysicalIndex: "velis-articles-v1-index" + string(rune('a'+index)),
			RequiredGeneration: generation, Status: status,
		})
	}
	return items
}

func TestConvergedStatusKeepsPendingWithoutIndexes(t *testing.T) {
	// OpenSearch 未配置或尚未初始化时没有 delivery：槽位必须保留待处理目标。
	if status := ConvergedStatus(1, nil); status != StatusPending {
		t.Fatalf("没有 delivery 时槽位必须保持待处理，实际 %s", status)
	}
	if status := ConvergedStatus(1, deliveriesOf(t, 1)); status != StatusPending {
		t.Fatalf("空 delivery 列表必须保持待处理，实际 %s", status)
	}
}

func TestConvergedStatusAggregatesDeliveries(t *testing.T) {
	cases := map[string]struct {
		statuses []Status
		want     Status
	}{
		"全部成功":      {[]Status{StatusSucceeded, StatusSucceeded}, StatusSucceeded},
		"一条在退避":     {[]Status{StatusSucceeded, StatusRetryWait}, StatusRetryWait},
		"两条都在退避":    {[]Status{StatusRetryWait, StatusRetryWait}, StatusRetryWait},
		"一条永久失败":    {[]Status{StatusSucceeded, StatusFailed}, StatusFailed},
		"部分已发出部分重试": {[]Status{StatusRunning, StatusRetryWait}, StatusPending},
		"一条成功一条未开始": {[]Status{StatusSucceeded, StatusPending}, StatusPending},
	}
	for name, item := range cases {
		if status := ConvergedStatus(3, deliveriesOf(t, 3, item.statuses...)); status != item.want {
			t.Fatalf("%s: 汇总状态 = %s，期望 %s", name, status, item.want)
		}
	}
}

func TestConvergedStatusTreatsStaleGenerationAsLagging(t *testing.T) {
	// 目标已推进到新 generation，但 delivery 还停在上一个 generation。
	stale := []Delivery{{ArticleID: 7, PhysicalIndex: "velis-articles-v1-indexa", RequiredGeneration: 2, Status: StatusSucceeded}}
	if status := ConvergedStatus(3, stale); status != StatusPending {
		t.Fatalf("落后 generation 的投递不得计入收敛，实际 %s", status)
	}
	fresh := []Delivery{{ArticleID: 7, PhysicalIndex: "velis-articles-v1-indexa", RequiredGeneration: 3, Status: StatusSucceeded}}
	if status := ConvergedStatus(3, fresh); status != StatusSucceeded {
		t.Fatalf("同一 generation 全部成功必须收敛，实际 %s", status)
	}
}

func TestNextDeliveryStatusClassifiesByResult(t *testing.T) {
	for _, result := range []Result{ResultCreated, ResultUpdated, ResultTombstoned, ResultNoop} {
		if status := NextDeliveryStatus(result, 3, 3); status != StatusSucceeded {
			t.Fatalf("%s 必须视为已收敛成功，实际 %s", result, status)
		}
	}
	for _, result := range []Result{ResultRetryable, ResultUnknown} {
		if status := NextDeliveryStatus(result, 1, 3); status != StatusRetryWait {
			t.Fatalf("%s 在未耗尽尝试时必须等待重试，实际 %s", result, status)
		}
		if status := NextDeliveryStatus(result, 3, 3); status != StatusFailed {
			t.Fatalf("%s 在耗尽尝试后必须永久失败，实际 %s", result, status)
		}
	}
	if status := NextDeliveryStatus(ResultPermanent, 1, 3); status != StatusFailed {
		t.Fatalf("永久失败不得重试，实际 %s", status)
	}
	if status := NextDeliveryStatus(ResultRetryable, 1, 0); status != StatusFailed {
		t.Fatalf("尝试上限为 0 时必须直接失败，实际 %s", status)
	}
}

func TestBackoffDelayGrowsExponentiallyAndCaps(t *testing.T) {
	base, max := time.Second, 8*time.Second
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second}
	for attempt, expected := range want {
		if delay := BackoffDelay(base, max, attempt+1); delay != expected {
			t.Fatalf("第 %d 次尝试退避 = %s，期望 %s", attempt+1, delay, expected)
		}
	}
	if delay := BackoffDelay(0, max, 1); delay != 0 {
		t.Fatalf("基数为 0 时不应退避，实际 %s", delay)
	}
}

func TestNewFailureTruncatesAndDropsEmptyCode(t *testing.T) {
	if failure := NewFailure("  ", "任意消息"); failure != nil {
		t.Fatalf("空错误码不应产生诊断: %+v", failure)
	}
	failure := NewFailure("connection_timeout", strings.Repeat("长", 600))
	if failure == nil {
		t.Fatal("缺失诊断")
	}
	if len(failure.Message) > MaxErrorMessageLength {
		t.Fatalf("错误摘要未限长: %d", len(failure.Message))
	}
	if !strings.HasPrefix(failure.Message, "长") || strings.HasSuffix(failure.Message, "\xef\xbf\xbd") {
		t.Fatalf("截断必须停在合法 UTF-8 边界: %q", failure.Message[len(failure.Message)-4:])
	}
}
