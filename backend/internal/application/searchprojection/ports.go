package searchprojection

import (
	"context"
	"errors"
	"time"

	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
)

// TargetStore 从 PostgreSQL 当前事实构造并推进每篇文章唯一的投影目标。
// 实现必须在调用方事务内生效：文章或 AI 选择与投影目标要么同时提交，要么同时回滚。
// 调用方不自行拼目标版本，也不能让业务事务连接 OpenSearch 或 RabbitMQ。
type TargetStore interface {
	// Advance 读取该文章当前事实（状态、当前修订与 AI 当前选择）并推进槽位。
	// 目标与槽位完全相同时是不做任何修改的 noop，返回 false。
	Advance(context.Context, int64, time.Time) (bool, error)
}

// SnapshotRequest 是按稳定 article ID 水位读取当前公开投影的分页请求。
type SnapshotRequest struct {
	AfterArticleID int64
	Limit          int
}

// ChangeRequest 是按全局 change sequence 扫描槽位的增量请求。
type ChangeRequest struct {
	AfterChangeSeq int64
	Limit          int
}

// JobPage 是一次增量扫描结果：只返回槽位目标，文档由执行器按当前事实重新构造。
type JobPage struct {
	Jobs         []ClaimedJob
	HasMore      bool
	HighWaterSeq int64
}

// ProjectionReader 读取 PostgreSQL 当前事实。
// 它只提供读取：投影结果不得反向修改业务事实。
type ProjectionReader interface {
	// Current 批量读取指定文章的当前投影；不存在或已物理删除的文章不出现在结果里。
	Current(context.Context, []int64) ([]CurrentProjection, error)
	// SnapshotPage 按 article ID 升序读取当前公开投影，供重建快照阶段使用。
	// 它不返回不可见文章：快照不把存量下架内容写进候选索引。
	SnapshotPage(context.Context, SnapshotRequest) ([]CurrentProjection, bool, error)
	// SinceChange 读取 change sequence 大于水位的槽位，供重建增量追赶使用。
	SinceChange(context.Context, ChangeRequest) (JobPage, error)
	// PublicCount 返回 PostgreSQL 中当前公开文章数，供校验与候选索引计数比对。
	PublicCount(context.Context) (int64, error)
	// Sample 以确定性顺序抽样公开文章，供身份与内容哈希校验。
	Sample(context.Context, int) ([]CurrentProjection, error)
}

// ClaimRequest 是一次有界认领；执行外部请求期间不得持有行锁。
type ClaimRequest struct {
	Owner     string
	Lease     time.Duration
	BatchSize int
	Now       time.Time
}

// Completion 是一次 fenced 写回：必须同时匹配文章、generation、租约 token 与物理索引。
// 尝试次数以实现读到的持久化值为准，调用方不自行声明。
type Completion struct {
	ArticleID     int64
	PhysicalIndex string
	Generation    int64
	LeaseToken    string
	Outcome       DeliveryOutcome
	MaxAttempts   int
	NextAttemptAt time.Time
	Now           time.Time
}

// ExecutionStore 是投影 Worker 的认领与写回端口。
type ExecutionStore interface {
	// Claim 认领有待处理 delivery 的到期槽位，推进为 running 并附带一次租约。
	Claim(context.Context, ClaimRequest) ([]ClaimedJob, error)
	// Complete 依据分类更新单个 delivery，并派生槽位总状态。
	// 匹配不到（租约被替代、generation 已增加）时返回 false，旧执行者不得覆盖新状态。
	Complete(context.Context, Completion) (bool, error)
}

// BulkItem 是待写入单个物理索引的一篇文档。
type BulkItem struct {
	PhysicalIndex string
	Document      Document
}

// DocumentWriter 批量写入投影文档。
// 分类在适配器边界完成：连接结果未知、限流与临时故障都必须是可重试分类，
// 只有严格映射失败或非法请求才是永久失败。返回的分类与 items 按下标一一对应。
type DocumentWriter interface {
	Bulk(context.Context, []BulkItem) ([]DeliveryOutcome, error)
}

// IndexSpec 是创建或校验物理索引所需的完整身份。
type IndexSpec struct {
	PhysicalIndex       string
	SchemaVersion       int
	SchemaIdentity      string
	EmbeddingDimensions int
}

// IndexState 是索引的真实状态，来自实际 settings/mapping 读取而不是索引名推断。
type IndexState struct {
	PhysicalIndex  string
	SchemaVersion  int
	SchemaIdentity string
	// Documents 是索引中的全部文档数；VisibleDocuments 只统计可检索文档。
	Documents        int64
	VisibleDocuments int64
	HasReadAlias     bool
	HasWriteAlias    bool
}

// AliasSwitch 是一次原子别名操作：把读写别名指向新索引，并同时解除旧索引的别名。
type AliasSwitch struct {
	ReadAlias   string
	WriteAlias  string
	Index       string
	DetachIndex string
}

// IndexAdmin 管理物理索引、模板与别名。
// 所有可能创建、切换或删除索引的操作都要求精确身份，不得只信任索引名。
type IndexAdmin interface {
	// Ensure 幂等初始化：目标索引与别名已满足时返回现状而不是创建重复索引。
	Ensure(context.Context, IndexSpec, AliasSwitch) (IndexState, error)
	// Inspect 读取实际 schema 身份与文档数；索引不存在时返回 ErrIndexMissing。
	Inspect(context.Context, string) (IndexState, error)
	// Create 创建物理索引；已存在且身份一致时视为幂等成功。
	Create(context.Context, IndexSpec) (IndexState, error)
	// SwitchAliases 用一次别名操作完成切换，避免读写别名短暂分离。
	SwitchAliases(context.Context, AliasSwitch) error
	// Delete 删除精确匹配的物理索引；调用方负责确认它不被别名或活动 delivery 引用。
	Delete(context.Context, string) error
	// Analyze 返回固定样例在指定分析器下的 token，供分析器契约测试核对。
	Analyze(context.Context, string, string, string) ([]string, error)
	// Fetch 读取索引中一篇文档的投影身份与内容指纹，供重建校验抽样比对。
	Fetch(context.Context, string, int64) (IndexedDocument, error)
	// Refresh 让已写入的文档对计数与抽样可见。计数与读取是近实时的，
	// 校验前必须刷新，否则刚写完的候选索引会被误判为空。
	Refresh(context.Context, string) error
}

// IndexedDocument 是索引中一篇文档的可比对身份；它不含正文。
type IndexedDocument struct {
	ArticleID   int64
	Generation  int64
	LockVersion int64
	RevisionID  int64
	Visible     bool
	ContentHash string
}

// ErrDocumentMissing 表示索引中不存在该文档。
var ErrDocumentMissing = errors.New("搜索索引中不存在该文档")

// RebuildStart 是一次重建开始的输入。
type RebuildStart struct {
	CandidateIndex      string
	TargetSchemaVersion int
	SchemaIdentity      string
	StartChangeSeq      int64
	Now                 time.Time
}

// RebuildUpdate 是重建状态的持久化更新；只允许按状态机前进。
type RebuildUpdate struct {
	ID                string
	Phase             projectionDomain.Phase
	SnapshotWatermark int64
	SnapshotDocuments int64
	CatchUpWatermark  int64
	Validation        *projectionDomain.ValidationReport
	CutoverAt         *time.Time
	RollbackDeadline  *time.Time
	LastError         string
	Now               time.Time
}

// RebuildStore 持久化重建状态与水位。
type RebuildStore interface {
	// Begin 在一个事务内抢占重建配额、创建候选索引记录并分配 start change sequence。
	// 已有活动重建或回滚窗口仍打开时返回 ErrRebuildConflict。
	Begin(context.Context, RebuildStart) (RebuildState, error)
	// Active 返回仍在生命周期内的重建记录（含回滚窗口内的 serving）；没有时返回 nil。
	// 它用于让 rollback / abandon / status 找到当前上下文，不表示仍占用重建配额。
	Active(context.Context) (*RebuildState, error)
	// CompleteRebuild 把已结束回滚窗口的重建记录标记为 completed。
	CompleteRebuild(context.Context, string, time.Time) error
	// Get 按 ID 读取重建记录。
	Get(context.Context, string) (RebuildState, error)
	// History 按开始时间倒序返回最近的记录，供 status 命令展示。
	History(context.Context, int) ([]RebuildState, error)
	// Update 按状态机持久化阶段与水位；非法转换返回 ErrInvalidRebuildPhase。
	Update(context.Context, RebuildUpdate) (RebuildState, error)
	// RegisterCandidate 把候选索引注册为全部槽位的第二活动 delivery，并把槽位拉回待处理。
	RegisterCandidate(context.Context, string, time.Time) (int64, error)
	// DetachCandidate 解除候选索引的全部 delivery，并把因此落后的槽位拉回待处理。
	DetachCandidate(context.Context, string, time.Time) (int64, error)
	// CloseRollbackWindow 结束回滚窗口并解除前一索引的 delivery。
	CloseRollbackWindow(context.Context, time.Time) error
	// EnsureSlots 为尚未建立槽位的文章创建待处理目标，并返回各文章的当前 generation。
	// 快照必须用它作为文档外部版本，否则后续更低 generation 的写入会被版本条件拒绝。
	EnsureSlots(context.Context, []int64, time.Time) ([]ArticleGeneration, error)
	// EnsureActiveDeliveries 为已有槽位补齐活动索引的 delivery；索引初始化后必须调用。
	EnsureActiveDeliveries(context.Context, time.Time) (int64, error)
	// SetState 持久化当前服务索引与回滚窗口；切换与回滚必须与别名操作成对出现。
	SetState(context.Context, IndexStateRow, time.Time) error
	// ConvergeCandidate 由 CLI 在候选索引上直接推进一个 delivery；CLI 不持有租约，
	// 因此只有槽位仍处于该 generation 时才生效，否则返回 false 并由后续增量扫描覆盖。
	ConvergeCandidate(context.Context, int64, string, int64, string, time.Time) (bool, error)
	// LaggingCandidateDeliveries 统计候选索引上尚未达到槽位当前 generation 的投递数量。
	LaggingCandidateDeliveries(context.Context, string) (int64, error)
	// ActiveLeases 统计仍在执行且租约未过期的槽位数量，供重建在切换前确认没有在途写入。
	ActiveLeases(context.Context, time.Time) (int64, error)
	// PendingCandidateDeliveries 按文章 ID 升序返回候选索引上仍待处理的槽位，
	// 供重建在增量扫描之外补齐从未变化的槽位。
	PendingCandidateDeliveries(context.Context, string, int) ([]ArticleGeneration, error)
	// IndexedDeliveries 统计某物理索引上被引用的 delivery 数量，供 cleanup 安全判断。
	IndexedDeliveries(context.Context, string) (int64, error)
}

// ArticleGeneration 是快照写入一篇文档所需的外部版本。
type ArticleGeneration struct {
	ArticleID  int64
	Generation int64
}

// RebuildState 是持久化的重建记录。
type RebuildState struct {
	ID                  string
	CandidateIndex      string
	TargetSchemaVersion int
	SchemaIdentity      string
	Phase               projectionDomain.Phase
	StartChangeSeq      int64
	SnapshotWatermark   int64
	SnapshotDocuments   int64
	CatchUpWatermark    int64
	Validation          *projectionDomain.ValidationReport
	ValidatedAt         *time.Time
	CutoverAt           *time.Time
	RollbackDeadline    *time.Time
	LastError           string
	StartedAt           time.Time
	UpdatedAt           time.Time
}

// IndexRegistry 读取当前索引服务状态；它决定哪些物理索引是合法的投递目标。
type IndexRegistry interface {
	// State 返回当前读写别名、当前服务索引以及回滚窗口内的前一索引。
	// 尚未初始化时返回 ErrIndexNotInitialized。
	State(context.Context) (IndexStateRow, error)
}

// IndexStateRow 是索引服务状态单行记录。
type IndexStateRow struct {
	ReadAlias        string
	WriteAlias       string
	CurrentIndex     string
	RollbackIndex    string
	RollbackDeadline *time.Time
	SchemaVersion    int
	SchemaIdentity   string
	UpdatedAt        time.Time
}

// Backlog 是投影积压快照，用于指标与日志；只包含有界分类与计数。
type Backlog struct {
	Pending           int64
	Failing           int64
	OldestSeconds     float64
	LaggingDeliveries int64
}

// Observer 暴露投影运行状态；实现不得让正文、HTML、向量或凭据成为标签。
type Observer interface {
	Claimed(job ClaimedJob, deliveries int)
	Delivered(job ClaimedJob, outcome DeliveryOutcome)
	Staled(job ClaimedJob, generation int64)
	Inconsistent(job ClaimedJob)
	Finished(job ClaimedJob, outcome DeliveryOutcome)
	Observe(backlog Backlog)
	Rebuilt(state RebuildState, action string)
}

// ObserverFuncs 让调用方只实现需要的回调。
type ObserverFuncs struct {
	OnClaimed      func(ClaimedJob, int)
	OnDelivered    func(ClaimedJob, DeliveryOutcome)
	OnStaled       func(ClaimedJob, int64)
	OnInconsistent func(ClaimedJob)
	OnFinished     func(ClaimedJob, DeliveryOutcome)
	OnObserve      func(Backlog)
	OnRebuilt      func(RebuildState, string)
}

func (f ObserverFuncs) Claimed(job ClaimedJob, deliveries int) {
	if f.OnClaimed != nil {
		f.OnClaimed(job, deliveries)
	}
}

func (f ObserverFuncs) Delivered(job ClaimedJob, outcome DeliveryOutcome) {
	if f.OnDelivered != nil {
		f.OnDelivered(job, outcome)
	}
}

func (f ObserverFuncs) Staled(job ClaimedJob, generation int64) {
	if f.OnStaled != nil {
		f.OnStaled(job, generation)
	}
}

func (f ObserverFuncs) Finished(job ClaimedJob, outcome DeliveryOutcome) {
	if f.OnFinished != nil {
		f.OnFinished(job, outcome)
	}
}

func (f ObserverFuncs) Inconsistent(job ClaimedJob) {
	if f.OnInconsistent != nil {
		f.OnInconsistent(job)
	}
}

func (f ObserverFuncs) Observe(backlog Backlog) {
	if f.OnObserve != nil {
		f.OnObserve(backlog)
	}
}

func (f ObserverFuncs) Rebuilt(state RebuildState, action string) {
	if f.OnRebuilt != nil {
		f.OnRebuilt(state, action)
	}
}
