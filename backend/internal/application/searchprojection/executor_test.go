package searchprojection

import (
	"context"
	"errors"
	"testing"
	"time"

	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
)

type fakeExecution struct {
	jobs        []ClaimedJob
	claimErr    error
	completed   []Completion
	results     []bool
	completeErr error
}

func (f *fakeExecution) Claim(context.Context, ClaimRequest) ([]ClaimedJob, error) {
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	jobs := f.jobs
	f.jobs = nil
	return jobs, nil
}

func (f *fakeExecution) Complete(_ context.Context, completion Completion) (bool, error) {
	f.completed = append(f.completed, completion)
	if f.completeErr != nil {
		return false, f.completeErr
	}
	if len(f.results) == 0 {
		return true, nil
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result, nil
}

type fakeReader struct {
	projections []CurrentProjection
	err         error
}

func (f *fakeReader) Current(context.Context, []int64) ([]CurrentProjection, error) {
	return f.projections, f.err
}
func (f *fakeReader) SnapshotPage(context.Context, SnapshotRequest) ([]CurrentProjection, bool, error) {
	return nil, false, nil
}
func (f *fakeReader) SinceChange(context.Context, ChangeRequest) (JobPage, error) {
	return JobPage{}, nil
}
func (f *fakeReader) PublicCount(context.Context) (int64, error) { return 0, nil }
func (f *fakeReader) Sample(context.Context, int) ([]CurrentProjection, error) {
	return nil, nil
}

type fakeTargets struct {
	advanced []int64
	err      error
}

func (f *fakeTargets) Advance(_ context.Context, articleID int64, _ time.Time) (bool, error) {
	f.advanced = append(f.advanced, articleID)
	return true, f.err
}

type fakeWriter struct {
	items    []BulkItem
	outcomes []DeliveryOutcome
	err      error
}

func (f *fakeWriter) Bulk(_ context.Context, items []BulkItem) ([]DeliveryOutcome, error) {
	f.items = append(f.items, items...)
	if f.err != nil {
		return nil, f.err
	}
	if f.outcomes != nil {
		return f.outcomes, nil
	}
	outcomes := make([]DeliveryOutcome, len(items))
	for index := range outcomes {
		outcomes[index] = DeliveryOutcome{Result: projectionDomain.ResultCreated}
	}
	return outcomes, nil
}

type observed struct {
	claimed      []int64
	staled       []int64
	inconsistent []int64
	delivered    []DeliveryOutcome
}

func newObserver() (*observed, Observer) {
	state := &observed{}
	return state, ObserverFuncs{
		OnClaimed:      func(job ClaimedJob, _ int) { state.claimed = append(state.claimed, job.ArticleID) },
		OnStaled:       func(job ClaimedJob, _ int64) { state.staled = append(state.staled, job.ArticleID) },
		OnInconsistent: func(job ClaimedJob) { state.inconsistent = append(state.inconsistent, job.ArticleID) },
		OnDelivered:    func(_ ClaimedJob, outcome DeliveryOutcome) { state.delivered = append(state.delivered, outcome) },
	}
}

func policy() ExecutorPolicy {
	return ExecutorPolicy{Lease: time.Minute, BatchSize: 10, MaxAttempts: 3,
		BackoffMin: time.Second, BackoffMax: time.Minute, SchemaVersion: 1, Owner: "worker-a"}
}

func targetFor(action projectionDomain.Action, lockVersion, revisionID int64) projectionDomain.Target {
	target, err := projectionDomain.NewTarget(action, lockVersion, revisionID, nil, nil)
	if err != nil {
		panic(err)
	}
	return target
}

func jobFor(articleID, generation int64, indexes ...string) ClaimedJob {
	job := ClaimedJob{ArticleID: articleID, Target: targetFor(projectionDomain.ActionUpsert, 1, articleID*10),
		Generation: generation, Lease: projectionDomain.Lease{Owner: "worker-a", Token: "token-1", ExpiresAt: time.Now().Add(time.Minute)}}
	for _, index := range indexes {
		job.Deliveries = append(job.Deliveries, projectionDomain.Delivery{ArticleID: articleID, PhysicalIndex: index,
			RequiredGeneration: generation, Status: projectionDomain.StatusRunning, Attempt: 1})
	}
	return job
}

func projectionFor(articleID, generation int64) CurrentProjection {
	return CurrentProjection{ArticleID: articleID,
		Target:   targetFor(projectionDomain.ActionUpsert, 1, articleID*10),
		Document: Document{ArticleID: articleID, LockVersion: 1, RevisionID: articleID * 10, Visible: true, Title: "标题"}}
}

func newTestExecutor(execution *fakeExecution, reader *fakeReader, targets *fakeTargets, writer *fakeWriter) (*Executor, *observed) {
	state, observer := newObserver()
	executor := NewExecutor(execution, reader, targets, writer, policy(), nil, nil).WithObserver(observer)
	return executor, state
}

// TestExecutorWritesDocumentAndWritesBackPerDelivery 覆盖 5.1 的主路径。
func TestExecutorWritesDocumentAndWritesBackPerDelivery(t *testing.T) {
	execution := &fakeExecution{jobs: []ClaimedJob{jobFor(7, 3, "index-a")}}
	reader := &fakeReader{projections: []CurrentProjection{projectionFor(7, 3)}}
	writer := &fakeWriter{}
	executor, state := newTestExecutor(execution, reader, &fakeTargets{}, writer)

	processed, err := executor.ProcessBatch(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("处理结果 processed=%d err=%v", processed, err)
	}
	if len(writer.items) != 1 || writer.items[0].PhysicalIndex != "index-a" {
		t.Fatalf("必须按 delivery 投递到对应物理索引: %+v", writer.items)
	}
	// 文档必须带上槽位 generation 与 schema 版本作为版本身份。
	if writer.items[0].Document.Generation != 3 || writer.items[0].Document.SchemaVersion != 1 {
		t.Fatalf("文档版本身份错误: %+v", writer.items[0].Document)
	}
	if len(execution.completed) != 1 || execution.completed[0].Generation != 3 ||
		execution.completed[0].LeaseToken != "token-1" || execution.completed[0].PhysicalIndex != "index-a" {
		t.Fatalf("写回必须 fenced 到文章、generation、token 与索引: %+v", execution.completed)
	}
	if len(state.delivered) != 1 || len(state.staled) != 0 {
		t.Fatalf("观测结果错误: %+v", state)
	}
}

// TestExecutorReplanesWhenFactsChangedBeforeSending 覆盖 5.1 的执行前复核：
// 事实已变化时不得发送旧目标，必须重新推进槽位并结束该次认领。
func TestExecutorReplanesWhenFactsChangedBeforeSending(t *testing.T) {
	execution := &fakeExecution{jobs: []ClaimedJob{jobFor(7, 3, "index-a")}}
	stale := projectionFor(7, 3)
	stale.Target = targetFor(projectionDomain.ActionUpsert, 5, 70) // 文章已被再次修订
	writer := &fakeWriter{}
	targets := &fakeTargets{}
	executor, state := newTestExecutor(execution, &fakeReader{projections: []CurrentProjection{stale}}, targets, writer)

	if _, err := executor.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(writer.items) != 0 {
		t.Fatalf("旧目标不得发送: %+v", writer.items)
	}
	if len(targets.advanced) != 1 || targets.advanced[0] != 7 {
		t.Fatalf("必须重新推进槽位: %+v", targets.advanced)
	}
	if len(execution.completed) != 0 || len(state.staled) != 1 {
		t.Fatalf("陈旧执行必须被观测且不写回: %+v %+v", execution.completed, state)
	}
}

// TestExecutorTreatsLateWriteBackAsStale 覆盖 5.1 的迟到完成：
// 租约被替代时旧执行者不得把槽位标记为成功。
func TestExecutorTreatsLateWriteBackAsStale(t *testing.T) {
	execution := &fakeExecution{jobs: []ClaimedJob{jobFor(7, 3, "index-a")}, results: []bool{false}}
	executor, state := newTestExecutor(execution, &fakeReader{projections: []CurrentProjection{projectionFor(7, 3)}}, &fakeTargets{}, &fakeWriter{})

	if _, err := executor.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(state.delivered) != 0 || len(state.staled) != 1 {
		t.Fatalf("迟到写回必须只记为陈旧: %+v", state)
	}
}

// TestExecutorIsolatesPartialFailureAcrossIndexes 覆盖 5.1 的双索引部分失败：
// 一个索引永久失败不得阻止另一个索引写回，两者的分类必须各自独立。
func TestExecutorIsolatesPartialFailureAcrossIndexes(t *testing.T) {
	execution := &fakeExecution{jobs: []ClaimedJob{jobFor(7, 3, "index-a", "index-b")}}
	writer := &fakeWriter{outcomes: []DeliveryOutcome{
		{Result: projectionDomain.ResultCreated},
		{Result: projectionDomain.ResultPermanent, Code: ErrorMapping, Message: "严格映射失败"},
	}}
	executor, state := newTestExecutor(execution, &fakeReader{projections: []CurrentProjection{projectionFor(7, 3)}}, &fakeTargets{}, writer)

	if _, err := executor.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(execution.completed) != 2 {
		t.Fatalf("两条 delivery 必须各自写回: %+v", execution.completed)
	}
	if execution.completed[0].PhysicalIndex != "index-a" || execution.completed[1].PhysicalIndex != "index-b" {
		t.Fatalf("写回顺序必须与投递顺序一致: %+v", execution.completed)
	}
	if len(state.delivered) != 2 || state.delivered[1].Code != ErrorMapping {
		t.Fatalf("必须逐项报告分类: %+v", state.delivered)
	}
}

// TestExecutorRetriesOnlyFailedDeliveriesAndKeepsOthers 覆盖 5.1 重试选择：
// 重试批次只包含未完成的 delivery。
func TestExecutorRetriesOnlyFailedDeliveriesAndKeepsOthers(t *testing.T) {
	execution := &fakeExecution{jobs: []ClaimedJob{jobFor(7, 3, "index-b")}}
	writer := &fakeWriter{outcomes: []DeliveryOutcome{{Result: projectionDomain.ResultUpdated}}}
	executor, _ := newTestExecutor(execution, &fakeReader{projections: []CurrentProjection{projectionFor(7, 3)}}, &fakeTargets{}, writer)

	if _, err := executor.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(writer.items) != 1 || writer.items[0].PhysicalIndex != "index-b" {
		t.Fatalf("必须只投递未完成的索引: %+v", writer.items)
	}
}

// TestExecutorRecordsInconsistentSelection 覆盖 5.1 的不一致选择：
// 不一致只作为可观测事件，文本投影仍然必须推进。
func TestExecutorRecordsInconsistentSelection(t *testing.T) {
	projection := projectionFor(7, 3)
	projection.InconsistentSelection = true
	projection.Document.Vector = nil
	execution := &fakeExecution{jobs: []ClaimedJob{jobFor(7, 3, "index-a")}}
	writer := &fakeWriter{}
	executor, state := newTestExecutor(execution, &fakeReader{projections: []CurrentProjection{projection}}, &fakeTargets{}, writer)

	if _, err := executor.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(state.inconsistent) != 1 {
		t.Fatalf("不一致选择必须可观测: %+v", state)
	}
	if len(writer.items) != 1 || len(writer.items[0].Document.Vector) != 0 {
		t.Fatalf("不一致的向量不得写入: %+v", writer.items)
	}
}

func TestExecutorSkipsWhenNothingClaimed(t *testing.T) {
	execution := &fakeExecution{}
	writer := &fakeWriter{}
	executor, _ := newTestExecutor(execution, &fakeReader{}, &fakeTargets{}, writer)
	processed, err := executor.ProcessBatch(context.Background())
	if err != nil || processed != 0 || len(writer.items) != 0 {
		t.Fatalf("空批次 processed=%d err=%v", processed, err)
	}
}

func TestExecutorPropagatesClaimFailure(t *testing.T) {
	execution := &fakeExecution{claimErr: errors.New("数据库不可用")}
	executor, _ := newTestExecutor(execution, &fakeReader{}, &fakeTargets{}, &fakeWriter{})
	if _, err := executor.ProcessBatch(context.Background()); err == nil {
		t.Fatal("认领失败必须向上报告")
	}
}
