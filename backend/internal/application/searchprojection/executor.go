package searchprojection

import (
	"context"
	"time"

	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
)

// ExecutorPolicy 是投影执行的边界；退避与尝试上限都来自配置。
type ExecutorPolicy struct {
	Lease         time.Duration
	BatchSize     int
	MaxAttempts   int
	BackoffMin    time.Duration
	BackoffMax    time.Duration
	SchemaVersion int
	Owner         string
}

// Executor 是投影 Worker 的核心：认领槽位、复核当前事实、按 delivery 投递并 fenced 写回。
// 它在外部请求期间不持有 PostgreSQL 行锁。
type Executor struct {
	execution ExecutionStore
	reader    ProjectionReader
	targets   TargetStore
	writer    DocumentWriter
	policy    ExecutorPolicy
	now       func() time.Time
	jitter    func(time.Duration) time.Duration
	observer  Observer
}

func NewExecutor(execution ExecutionStore, reader ProjectionReader, targets TargetStore, writer DocumentWriter,
	policy ExecutorPolicy, now func() time.Time, jitter func(time.Duration) time.Duration) *Executor {
	if now == nil {
		now = time.Now
	}
	if jitter == nil {
		jitter = func(delay time.Duration) time.Duration { return delay }
	}
	return &Executor{execution: execution, reader: reader, targets: targets, writer: writer,
		policy: policy, now: now, jitter: jitter}
}

// WithObserver 挂载只读观测；它不得改变执行结果。
func (e *Executor) WithObserver(observer Observer) *Executor {
	e.observer = observer
	return e
}

// ProcessBatch 处理一批到期槽位，返回本批认领到的槽位数量。
// 单个槽位的失败永远不会终止整批：每个 delivery 独立分类与写回。
func (e *Executor) ProcessBatch(ctx context.Context) (int, error) {
	now := e.now().UTC()
	jobs, err := e.execution.Claim(ctx, ClaimRequest{
		Owner: e.policy.Owner, Lease: e.policy.Lease, BatchSize: e.policy.BatchSize, Now: now,
	})
	if err != nil || len(jobs) == 0 {
		return 0, err
	}
	for _, job := range jobs {
		if e.observer != nil {
			e.observer.Claimed(job, len(job.Deliveries))
		}
	}

	identifiers := make([]int64, 0, len(jobs))
	for _, job := range jobs {
		identifiers = append(identifiers, job.ArticleID)
	}
	projections, err := e.reader.Current(ctx, identifiers)
	if err != nil {
		return len(jobs), err
	}
	current := make(map[int64]CurrentProjection, len(projections))
	for _, projection := range projections {
		current[projection.ArticleID] = projection
	}

	items := make([]BulkItem, 0, len(jobs))
	planned := make([]plannedDelivery, 0, len(jobs))
	stale := make([]ClaimedJob, 0)
	for _, job := range jobs {
		projection, ok := current[job.ArticleID]
		// 执行前复核：数据库当前事实与槽位目标不一致时不得发送旧目标。
		if !ok || !projection.Target.Equal(job.Target) {
			stale = append(stale, job)
			continue
		}
		document := projection.Document
		document.Generation = job.Generation
		document.SchemaVersion = e.policy.SchemaVersion
		// 内容指纹必须由所有写入路径统一计算，否则重建校验抽样会比不出真实差异。
		document.ContentHash = DocumentFingerprint(document)
		if projection.InconsistentSelection && e.observer != nil {
			// 不一致的向量已被读取层忽略；这里只把它作为可观测事件记录下来。
			e.observer.Inconsistent(job)
		}
		for _, delivery := range job.Deliveries {
			items = append(items, BulkItem{PhysicalIndex: delivery.PhysicalIndex, Document: document})
			planned = append(planned, plannedDelivery{
				articleID: job.ArticleID, index: delivery.PhysicalIndex,
				generation: job.Generation, leaseToken: job.Lease.Token, attempt: delivery.Attempt,
			})
		}
	}
	if err := e.replanStale(ctx, stale, now); err != nil {
		return len(jobs), err
	}
	if len(items) == 0 {
		return len(jobs), nil
	}
	outcomes, err := e.writer.Bulk(ctx, items)
	if err != nil {
		return len(jobs), err
	}
	for index, outcome := range outcomes {
		if index >= len(planned) {
			break
		}
		delivery := planned[index]
		delivery.outcome = outcome
		if err := e.writeBack(ctx, delivery); err != nil {
			return len(jobs), err
		}
	}
	return len(jobs), nil
}

type plannedDelivery struct {
	articleID  int64
	index      string
	generation int64
	leaseToken string
	attempt    int
	outcome    DeliveryOutcome
}

// writeBack 按 (article, generation, token, index) fenced 写回单个 delivery。
func (e *Executor) writeBack(ctx context.Context, delivery plannedDelivery) error {
	now := e.now().UTC()
	delay := projectionDomain.BackoffDelay(e.policy.BackoffMin, e.policy.BackoffMax, delivery.attempt)
	ok, err := e.execution.Complete(ctx, Completion{
		ArticleID: delivery.articleID, PhysicalIndex: delivery.index, Generation: delivery.generation,
		LeaseToken: delivery.leaseToken, Outcome: delivery.outcome, MaxAttempts: e.policy.MaxAttempts,
		NextAttemptAt: now.Add(e.jitter(delay)), Now: now,
	})
	if e.observer == nil {
		return err
	}
	job := ClaimedJob{ArticleID: delivery.articleID, Generation: delivery.generation}
	if err != nil {
		return err
	}
	if !ok {
		// 租约已被替代或 generation 已增加：旧执行者不得覆盖新状态，
		// 本次投递不能被计入成功。
		e.observer.Staled(job, delivery.generation)
		return nil
	}
	e.observer.Delivered(job, delivery.outcome)
	return nil
}

// replanStale 用短事务把事实已变化的槽位重新推进到当前事实，并丢弃已构造的旧操作。
func (e *Executor) replanStale(ctx context.Context, stale []ClaimedJob, now time.Time) error {
	for _, job := range stale {
		if _, err := e.targets.Advance(ctx, job.ArticleID, now); err != nil {
			return err
		}
		if e.observer != nil {
			e.observer.Staled(job, job.Generation)
		}
	}
	return nil
}
