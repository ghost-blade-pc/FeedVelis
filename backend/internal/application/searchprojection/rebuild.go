package searchprojection

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
)

// maxCatchUpRounds 与 maxCatchUpPages 限制增量追赶的重复扫描次数与页数。
// 持续写入时追赶可能永远追不上高水位，重建必须有界退出并把「仍有落后投递」
// 留给校验阶段显式拒绝，而不是无限循环。
const (
	maxCatchUpRounds = 20
	maxCatchUpPages  = 200
)

// RebuildPolicy 是索引重建的边界；它必须与当前配置的 schema 身份一致。
type RebuildPolicy struct {
	IndexPrefix         string
	SchemaVersion       int
	SchemaIdentity      string
	EmbeddingDimensions int
	SnapshotBatch       int
	RollbackWindow      time.Duration
	Lease               time.Duration
	SampleSize          int
	PollInterval        time.Duration
}

// RebuildService 编排可恢复的索引重建：快照、增量追赶、校验、切换与回滚。
// 它不覆盖当前读索引：所有写入都发生在候选索引上，直到一次原子别名切换。
type RebuildService struct {
	store    RebuildStore
	reader   ProjectionReader
	index    IndexAdmin
	writer   DocumentWriter
	registry IndexRegistry
	policy   RebuildPolicy
	now      func() time.Time
	// buildID 生成物理索引的构建标识；切换为固定实现以便测试注入。
	buildID  func(time.Time) string
	observer Observer
}

func NewRebuildService(store RebuildStore, reader ProjectionReader, index IndexAdmin, writer DocumentWriter,
	registry IndexRegistry, policy RebuildPolicy, now func() time.Time) *RebuildService {
	if now == nil {
		now = time.Now
	}
	service := &RebuildService{store: store, reader: reader, index: index, writer: writer,
		registry: registry, policy: policy, now: now}
	service.buildID = defaultBuildID
	return service
}

// WithObserver 挂载只读观测。
func (s *RebuildService) WithObserver(observer Observer) *RebuildService {
	s.observer = observer
	return s
}

// WithBuildID 替换构建标识生成器，仅供测试注入确定性取值。
func (s *RebuildService) WithBuildID(buildID func(time.Time) string) *RebuildService {
	s.buildID = buildID
	return s
}

func defaultBuildID(now time.Time) string {
	return fmt.Sprintf("%s-%s", now.UTC().Format("20060102t150405z"), randomSuffix())
}

// randomSuffix 返回 6 位小写十六进制后缀，使物理索引名在同一秒内也不会冲突。
func randomSuffix() string {
	var buffer [3]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return fmt.Sprintf("%06x", time.Now().UnixNano()&0xffffff)
	}
	return hex.EncodeToString(buffer[:])
}

// IndexStatus 是索引与重建的当前状态快照，供 status 命令输出。
type IndexStatus struct {
	State   IndexStateRow
	Active  *RebuildState
	History []RebuildState
}

// spec 构造候选索引的完整身份。
func (s *RebuildService) spec(index string) IndexSpec {
	return IndexSpec{PhysicalIndex: index, SchemaVersion: s.policy.SchemaVersion,
		SchemaIdentity: s.policy.SchemaIdentity, EmbeddingDimensions: s.policy.EmbeddingDimensions}
}

func (s *RebuildService) aliases(current, index string) AliasSwitch {
	return AliasSwitch{ReadAlias: s.policy.IndexPrefix + "-read", WriteAlias: s.policy.IndexPrefix + "-write",
		Index: index, DetachIndex: current}
}

// InitIndex 建立首个物理索引与读写别名，并让已有槽位获得投递目标。
// 重复执行是幂等的：不会创建重复索引或第二个写索引。
func (s *RebuildService) InitIndex(ctx context.Context) (IndexState, error) {
	now := s.now().UTC()
	// 已经服务同一 schema 时返回现状：不得创建重复索引或第二个写索引。
	// schema 身份必须来自实际 mapping，不能只信任数据库里的记录。
	if current, err := s.registry.State(ctx); err == nil {
		if state, inspectErr := s.index.Inspect(ctx, current.CurrentIndex); inspectErr == nil &&
			state.SchemaVersion == s.policy.SchemaVersion && state.SchemaIdentity == s.policy.SchemaIdentity {
			return state, nil
		}
	} else if !errors.Is(err, ErrIndexNotInitialized) {
		return IndexState{}, err
	}
	name := fmt.Sprintf("%s-v%d-%s", s.policy.IndexPrefix, s.policy.SchemaVersion, s.buildID(now))
	if _, err := s.index.Ensure(ctx, s.spec(name), s.aliases("", name)); err != nil {
		return IndexState{}, err
	}
	if err := s.store.SetState(ctx, IndexStateRow{
		ReadAlias: s.policy.IndexPrefix + "-read", WriteAlias: s.policy.IndexPrefix + "-write",
		CurrentIndex: name, SchemaVersion: s.policy.SchemaVersion, SchemaIdentity: s.policy.SchemaIdentity,
	}, now); err != nil {
		return IndexState{}, err
	}
	// 已有槽位必须立刻获得当前索引的投递目标，否则它们永远无法收敛。
	if _, err := s.store.EnsureActiveDeliveries(ctx, now); err != nil {
		return IndexState{}, err
	}
	return s.index.Inspect(ctx, name)
}

// Start 创建候选索引、抢占重建配额、注册第二 delivery 并立即推进各阶段。
func (s *RebuildService) Start(ctx context.Context) (RebuildState, error) {
	now := s.now().UTC()
	current, err := s.registry.State(ctx)
	if err != nil {
		return RebuildState{}, err
	}
	// 校验当前服务索引的实际 schema 身份：不能只信任数据库里的记录。
	state, err := s.index.Inspect(ctx, current.CurrentIndex)
	if err != nil {
		return RebuildState{}, err
	}
	if state.SchemaVersion != s.policy.SchemaVersion || state.SchemaIdentity != s.policy.SchemaIdentity {
		return RebuildState{}, fmt.Errorf("%w: 当前索引 %s 的身份为 %s",
			ErrSchemaMismatch, current.CurrentIndex, state.SchemaIdentity)
	}
	candidate := fmt.Sprintf("%s-v%d-%s", s.policy.IndexPrefix, s.policy.SchemaVersion, s.buildID(now))
	if _, err := s.index.Create(ctx, s.spec(candidate)); err != nil {
		return RebuildState{}, err
	}
	rebuild, err := s.store.Begin(ctx, RebuildStart{CandidateIndex: candidate,
		TargetSchemaVersion: s.policy.SchemaVersion, SchemaIdentity: s.policy.SchemaIdentity, Now: now})
	if err != nil {
		return RebuildState{}, err
	}
	if _, err := s.store.RegisterCandidate(ctx, candidate, now); err != nil {
		return RebuildState{}, err
	}
	s.observe(rebuild, "start")
	return s.run(ctx, rebuild)
}

// Resume 从持久化水位继续同一目标索引的重建。
func (s *RebuildService) Resume(ctx context.Context, id string) (RebuildState, error) {
	state, err := s.rebuildState(ctx, id)
	if err != nil {
		return RebuildState{}, err
	}
	if state.Phase.Terminal() {
		return RebuildState{}, fmt.Errorf("%w: %s 已结束", ErrInvalidRebuildPhase, id)
	}
	return s.run(ctx, state)
}

func (s *RebuildService) rebuildState(ctx context.Context, id string) (RebuildState, error) {
	if strings.TrimSpace(id) == "" {
		active, err := s.store.Active(ctx)
		if err != nil {
			return RebuildState{}, err
		}
		if active == nil {
			return RebuildState{}, ErrRebuildNotFound
		}
		return *active, nil
	}
	return s.store.Get(ctx, id)
}

// run 依次推进快照、增量追赶与校验；每个阶段都从持久化水位继续。
func (s *RebuildService) run(ctx context.Context, state RebuildState) (RebuildState, error) {
	switch state.Phase {
	case projectionDomain.PhaseSnapshot:
		next, err := s.snapshot(ctx, state)
		if err != nil {
			return s.fail(ctx, state, err)
		}
		state = next
		fallthrough
	case projectionDomain.PhaseCatchUp:
		next, err := s.catchUp(ctx, state)
		if err != nil {
			return s.fail(ctx, state, err)
		}
		state = next
		fallthrough
	case projectionDomain.PhaseValidate, projectionDomain.PhaseValidated:
		next, err := s.validate(ctx, state)
		if err != nil {
			return s.fail(ctx, state, err)
		}
		state = next
	}
	return state, nil
}

// snapshot 按持久化 article ID 水位把当前公开投影写入候选索引。
// 水位只在一个批次全部成功写入后前进：中途退出时 resume 会从上一个完整批次继续。
func (s *RebuildService) snapshot(ctx context.Context, state RebuildState) (RebuildState, error) {
	watermark, documents := state.SnapshotWatermark, state.SnapshotDocuments
	for {
		page, hasMore, err := s.reader.SnapshotPage(ctx, SnapshotRequest{AfterArticleID: watermark, Limit: s.policy.SnapshotBatch})
		if err != nil {
			return state, err
		}
		if len(page) == 0 {
			break
		}
		now := s.now().UTC()
		identifiers := make([]int64, 0, len(page))
		for _, projection := range page {
			identifiers = append(identifiers, projection.ArticleID)
		}
		// 快照文档的外部版本必须来自槽位 generation，否则后续更低的写入会被版本条件拒绝。
		generations, err := s.store.EnsureSlots(ctx, identifiers, now)
		if err != nil {
			return state, err
		}
		byArticle := make(map[int64]int64, len(generations))
		for _, item := range generations {
			byArticle[item.ArticleID] = item.Generation
		}
		items := make([]BulkItem, 0, len(page))
		for _, projection := range page {
			generation, ok := byArticle[projection.ArticleID]
			if !ok {
				continue
			}
			items = append(items, BulkItem{PhysicalIndex: state.CandidateIndex,
				Document: s.document(projection.Document, generation)})
		}
		if len(items) > 0 {
			outcomes, err := s.writer.Bulk(ctx, items)
			if err != nil {
				return state, err
			}
			for index, outcome := range outcomes {
				normalized := outcome.Normalize()
				if !normalized.Result.Applied() {
					return state, fmt.Errorf("快照写入候选索引失败: %s", normalized.Code)
				}
				// 快照直接写候选索引，因此必须同时把它对应的投递标记为已收敛：
				// 否则校验阶段会一直看到从未参与过增量扫描的落后投递。
				if _, err := s.store.ConvergeCandidate(ctx, items[index].Document.ArticleID, state.CandidateIndex,
					items[index].Document.Generation, string(normalized.Result), now); err != nil {
					return state, err
				}
				if items[index].Document.ArticleID > watermark {
					watermark = items[index].Document.ArticleID
				}
			}
			documents += int64(len(items))
		}
		state, err = s.store.Update(ctx, RebuildUpdate{ID: state.ID, Phase: projectionDomain.PhaseSnapshot,
			SnapshotWatermark: watermark, SnapshotDocuments: documents, Now: now})
		if err != nil {
			return state, err
		}
		if !hasMore {
			break
		}
	}
	return s.store.Update(ctx, RebuildUpdate{ID: state.ID, Phase: projectionDomain.PhaseCatchUp,
		SnapshotWatermark: watermark, SnapshotDocuments: documents,
		CatchUpWatermark: state.StartChangeSeq, Now: s.now().UTC()})
}

// catchUp 扫描 change sequence 之后的目标变化，把候选索引收敛到切换前的当前事实。
func (s *RebuildService) catchUp(ctx context.Context, state RebuildState) (RebuildState, error) {
	// 注册前发放的租约可能仍在执行：等待它们过期或完成，再进入校验。
	if err := s.waitLeaseBarrier(ctx, state); err != nil {
		return state, err
	}
	// 从未发生过目标变化的槽位不会出现在 change sequence 扫描里。
	// 已公开文章的文档由快照直接写入；不可见文章在候选索引中本就不应有文档，
	// 因此这里只把它们按 noop 收敛，避免校验阶段看到永远无法完成的投递。
	if err := s.drainUntouched(ctx, state); err != nil {
		return state, err
	}
	watermark := state.CatchUpWatermark
	if watermark < state.StartChangeSeq {
		watermark = state.StartChangeSeq
	}
	for round := 0; round < maxCatchUpRounds; round++ {
		for page := 0; page < maxCatchUpPages; page++ {
			batch, err := s.reader.SinceChange(ctx, ChangeRequest{AfterChangeSeq: watermark, Limit: s.policy.SnapshotBatch})
			if err != nil {
				return state, err
			}
			if len(batch.Jobs) == 0 {
				break
			}
			now := s.now().UTC()
			identifiers := make([]int64, 0, len(batch.Jobs))
			for _, job := range batch.Jobs {
				identifiers = append(identifiers, job.ArticleID)
			}
			projections, err := s.reader.Current(ctx, identifiers)
			if err != nil {
				return state, err
			}
			current := make(map[int64]CurrentProjection, len(projections))
			for _, projection := range projections {
				current[projection.ArticleID] = projection
			}
			items := make([]BulkItem, 0, len(batch.Jobs))
			expected := make([]ClaimedJob, 0, len(batch.Jobs))
			for _, job := range batch.Jobs {
				projection, ok := current[job.ArticleID]
				// 事实又变化时不在本轮写入：槽位已推进，下一轮扫描会覆盖。
				if !ok || !projection.Target.Equal(job.Target) {
					continue
				}
				items = append(items, BulkItem{PhysicalIndex: state.CandidateIndex,
					Document: s.document(projection.Document, job.Generation)})
				expected = append(expected, job)
			}
			if len(items) > 0 {
				outcomes, err := s.writer.Bulk(ctx, items)
				if err != nil {
					return state, err
				}
				for index, outcome := range outcomes {
					normalized := outcome.Normalize()
					if !normalized.Result.Applied() {
						// 单篇失败不阻塞整轮：下一轮仍会重新扫描该槽位。
						continue
					}
					result := string(normalized.Result)
					if _, err := s.store.ConvergeCandidate(ctx, expected[index].ArticleID, state.CandidateIndex,
						expected[index].Generation, result, now); err != nil {
						return state, err
					}
				}
			}
			watermark = batch.HighWaterSeq
			state, err = s.store.Update(ctx, RebuildUpdate{ID: state.ID, Phase: projectionDomain.PhaseCatchUp,
				CatchUpWatermark: watermark, Now: now})
			if err != nil {
				return state, err
			}
		}
		lagging, err := s.store.LaggingCandidateDeliveries(ctx, state.CandidateIndex)
		if err != nil {
			return state, err
		}
		if lagging == 0 {
			break
		}
		// 仍有落后投递：可能是 Worker 正在追赶，稍后重新扫描。
		if err := s.pause(ctx); err != nil {
			return state, err
		}
	}
	return s.store.Update(ctx, RebuildUpdate{ID: state.ID, Phase: projectionDomain.PhaseValidate,
		CatchUpWatermark: watermark, Now: s.now().UTC()})
}

// drainUntouched 收敛快照与增量扫描之外仍未处理的槽位。
func (s *RebuildService) drainUntouched(ctx context.Context, state RebuildState) error {
	for round := 0; round < maxCatchUpRounds; round++ {
		pending, err := s.store.PendingCandidateDeliveries(ctx, state.CandidateIndex, s.policy.SnapshotBatch)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		now := s.now().UTC()
		identifiers := make([]int64, 0, len(pending))
		for _, item := range pending {
			identifiers = append(identifiers, item.ArticleID)
		}
		projections, err := s.reader.Current(ctx, identifiers)
		if err != nil {
			return err
		}
		current := make(map[int64]CurrentProjection, len(projections))
		for _, projection := range projections {
			current[projection.ArticleID] = projection
		}
		// 仍公开且事实与槽位一致的槽位必须真正写入；其余（主要是 tombstone）按 noop 收敛。
		items := make([]BulkItem, 0, len(pending))
		written := make([]ArticleGeneration, 0, len(pending))
		for _, item := range pending {
			projection, ok := current[item.ArticleID]
			// 事实与槽位不一致时不写入：槽位已推进，下一轮会覆盖。
			if !ok {
				continue
			}
			if !projection.Document.Visible {
				if _, err := s.store.ConvergeCandidate(ctx, item.ArticleID, state.CandidateIndex, item.Generation,
					string(projectionDomain.ResultNoop), now); err != nil {
					return err
				}
				continue
			}
			items = append(items, BulkItem{PhysicalIndex: state.CandidateIndex,
				Document: s.document(projection.Document, item.Generation)})
			written = append(written, item)
		}
		if len(items) > 0 {
			outcomes, err := s.writer.Bulk(ctx, items)
			if err != nil {
				return err
			}
			for index, outcome := range outcomes {
				normalized := outcome.Normalize()
				if !normalized.Result.Applied() {
					continue
				}
				if _, err := s.store.ConvergeCandidate(ctx, written[index].ArticleID, state.CandidateIndex,
					written[index].Generation, string(normalized.Result), now); err != nil {
					return err
				}
			}
		}
		if err := s.pause(ctx); err != nil {
			return err
		}
	}
	return nil
}

// waitLeaseBarrier 等待注册前发放的租约结束：注册时可能已有 Worker 在途，
// 它们的写入结果不能进入校验。等待以「没有未过期的活动租约」为准，
// 空闲时立即返回，繁忙时最多等待一个租约周期。
func (s *RebuildService) waitLeaseBarrier(ctx context.Context, state RebuildState) error {
	if s.policy.Lease <= 0 {
		return nil
	}
	deadline := state.StartedAt.Add(s.policy.Lease)
	for {
		active, err := s.store.ActiveLeases(ctx, s.now().UTC())
		if err != nil {
			return err
		}
		if active == 0 || !s.now().UTC().Before(deadline) {
			return nil
		}
		if err := s.pause(ctx); err != nil {
			return err
		}
	}
}

func (s *RebuildService) pause(ctx context.Context) error {
	interval := s.policy.PollInterval
	if interval <= 0 {
		interval = time.Second
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// validate 比对公开文档数与抽样身份/内容指纹，并确认没有落后投递。
func (s *RebuildService) validate(ctx context.Context, state RebuildState) (RebuildState, error) {
	now := s.now().UTC()
	publicDocuments, err := s.reader.PublicCount(ctx)
	if err != nil {
		return state, err
	}
	candidate, err := s.index.Inspect(ctx, state.CandidateIndex)
	if err != nil {
		return state, err
	}
	lagging, err := s.store.LaggingCandidateDeliveries(ctx, state.CandidateIndex)
	if err != nil {
		return state, err
	}
	report := projectionDomain.ValidationReport{PublicDocuments: publicDocuments,
		CandidateDocuments: candidate.VisibleDocuments, LaggingDeliveries: lagging, ReportedAt: now}
	if sample := s.sampleMismatches(ctx, state); len(sample) > 0 {
		report.Mismatches = sample
	} else {
		report.Mismatches = []string{}
	}
	report.Sampled = s.policy.SampleSize
	if !report.Passed() {
		// 校验失败必须保留当前索引继续服务，并且不推进阶段。
		if _, err := s.store.Update(ctx, RebuildUpdate{ID: state.ID, Phase: projectionDomain.PhaseValidate,
			Validation: &report, LastError: "校验未通过，已拒绝别名切换", Now: now}); err != nil {
			return state, err
		}
		return state, fmt.Errorf("%w: 公开 %d 候选 %d 落后投递 %d 抽样不一致 %d",
			ErrValidationFailed, report.PublicDocuments, report.CandidateDocuments, report.LaggingDeliveries, len(report.Mismatches))
	}
	return s.store.Update(ctx, RebuildUpdate{ID: state.ID, Phase: projectionDomain.PhaseValidated,
		Validation: &report, LastError: "", Now: now})
}

// sampleMismatches 对确定性抽样比较投影身份与内容指纹；返回限长的不一致描述。
func (s *RebuildService) sampleMismatches(ctx context.Context, state RebuildState) []string {
	limit := s.policy.SampleSize
	if limit < 1 {
		return nil
	}
	projections, err := s.reader.Sample(ctx, limit)
	if err != nil {
		return []string{"抽样读取失败"}
	}
	identifiers := make([]int64, 0, len(projections))
	for _, projection := range projections {
		identifiers = append(identifiers, projection.ArticleID)
	}
	generations, err := s.store.EnsureSlots(ctx, identifiers, s.now().UTC())
	if err != nil {
		return []string{"抽样槽位读取失败"}
	}
	byArticle := make(map[int64]int64, len(generations))
	for _, item := range generations {
		byArticle[item.ArticleID] = item.Generation
	}
	mismatches := make([]string, 0, 8)
	for _, projection := range projections {
		expected := s.document(projection.Document, byArticle[projection.ArticleID])
		indexed, err := s.index.Fetch(ctx, state.CandidateIndex, projection.ArticleID)
		if err != nil {
			if len(mismatches) < 8 {
				mismatches = append(mismatches, fmt.Sprintf("article %d: 候选索引缺少文档", projection.ArticleID))
			}
			continue
		}
		if !indexed.Visible || indexed.LockVersion != expected.LockVersion || indexed.RevisionID != expected.RevisionID ||
			indexed.ContentHash != expected.ContentHash {
			if len(mismatches) < 8 {
				mismatches = append(mismatches, fmt.Sprintf("article %d: 候选索引内容与当前事实不一致", projection.ArticleID))
			}
		}
	}
	return mismatches
}

// Cutover 用一次别名操作把读写别名切到候选索引，并打开有界回滚窗口。
func (s *RebuildService) Cutover(ctx context.Context, id string) (RebuildState, error) {
	now := s.now().UTC()
	state, err := s.rebuildState(ctx, id)
	if err != nil {
		return RebuildState{}, err
	}
	// 只有通过校验的阶段才允许切换：校验失败会把阶段退回 validate，从而被这里拒绝。
	if state.Phase != projectionDomain.PhaseValidated {
		return RebuildState{}, fmt.Errorf("%w: 阶段 %s 不允许切换，必须先通过校验", ErrInvalidRebuildPhase, state.Phase)
	}
	// 切换前再次确认没有落后投递：校验之后、切换之前仍可能有新的变化。
	lagging, err := s.store.LaggingCandidateDeliveries(ctx, state.CandidateIndex)
	if err != nil {
		return RebuildState{}, err
	}
	if lagging != 0 {
		return RebuildState{}, fmt.Errorf("%w: 候选索引仍有 %d 条落后投递", ErrValidationFailed, lagging)
	}
	current, err := s.registry.State(ctx)
	if err != nil {
		return RebuildState{}, err
	}
	// 先进入切换阶段，再执行一次原子别名操作：阶段本身记录切换正在进行。
	if state.Phase != projectionDomain.PhaseCutover {
		if state, err = s.store.Update(ctx, RebuildUpdate{ID: state.ID,
			Phase: projectionDomain.PhaseCutover, Now: now}); err != nil {
			return RebuildState{}, err
		}
	}
	if err := s.index.SwitchAliases(ctx, AliasSwitch{
		ReadAlias: current.ReadAlias, WriteAlias: current.WriteAlias,
		Index: state.CandidateIndex, DetachIndex: current.CurrentIndex,
	}); err != nil {
		return RebuildState{}, err
	}
	deadline := now.Add(s.policy.RollbackWindow)
	if err := s.store.SetState(ctx, IndexStateRow{
		ReadAlias: current.ReadAlias, WriteAlias: current.WriteAlias, CurrentIndex: state.CandidateIndex,
		RollbackIndex: current.CurrentIndex, RollbackDeadline: &deadline,
		SchemaVersion: state.TargetSchemaVersion, SchemaIdentity: state.SchemaIdentity,
	}, now); err != nil {
		return RebuildState{}, err
	}
	updated, err := s.store.Update(ctx, RebuildUpdate{ID: state.ID, Phase: projectionDomain.PhaseServing,
		CutoverAt: &now, RollbackDeadline: &deadline, Now: now})
	if err != nil {
		return RebuildState{}, err
	}
	s.observe(updated, "cutover")
	return updated, nil
}

// Rollback 在回滚窗口内原子切回前一索引，并释放候选索引。
func (s *RebuildService) Rollback(ctx context.Context, id string) (RebuildState, error) {
	now := s.now().UTC()
	state, err := s.rebuildState(ctx, id)
	if err != nil {
		return RebuildState{}, err
	}
	if state.Phase != projectionDomain.PhaseServing {
		return RebuildState{}, fmt.Errorf("%w: 阶段 %s 不允许回滚", ErrInvalidRebuildPhase, state.Phase)
	}
	current, err := s.registry.State(ctx)
	if err != nil {
		return RebuildState{}, err
	}
	if current.RollbackIndex == "" {
		return RebuildState{}, fmt.Errorf("%w: 回滚窗口已关闭", ErrUnsafeIndexTarget)
	}
	if current.RollbackDeadline != nil && now.After(*current.RollbackDeadline) {
		return RebuildState{}, fmt.Errorf("%w: 回滚窗口已于 %s 结束", ErrUnsafeIndexTarget, current.RollbackDeadline.UTC().Format(time.RFC3339))
	}
	if err := s.index.SwitchAliases(ctx, AliasSwitch{
		ReadAlias: current.ReadAlias, WriteAlias: current.WriteAlias,
		Index: current.RollbackIndex, DetachIndex: state.CandidateIndex,
	}); err != nil {
		return RebuildState{}, err
	}
	if err := s.store.SetState(ctx, IndexStateRow{
		ReadAlias: current.ReadAlias, WriteAlias: current.WriteAlias, CurrentIndex: current.RollbackIndex,
		SchemaVersion: current.SchemaVersion, SchemaIdentity: current.SchemaIdentity,
	}, now); err != nil {
		return RebuildState{}, err
	}
	if _, err := s.store.DetachCandidate(ctx, state.CandidateIndex, now); err != nil {
		return RebuildState{}, err
	}
	updated, err := s.store.Update(ctx, RebuildUpdate{ID: state.ID, Phase: projectionDomain.PhaseAbandoned,
		LastError: "已回滚到前一索引", Now: now})
	if err != nil {
		return RebuildState{}, err
	}
	s.observe(updated, "rollback")
	return updated, nil
}

// Abandon 显式放弃未发布的候选索引：解除它的投递，但不删除物理索引。
func (s *RebuildService) Abandon(ctx context.Context, id string) (RebuildState, error) {
	now := s.now().UTC()
	state, err := s.rebuildState(ctx, id)
	if err != nil {
		return RebuildState{}, err
	}
	if _, err := s.store.DetachCandidate(ctx, state.CandidateIndex, now); err != nil {
		return RebuildState{}, err
	}
	updated, err := s.store.Update(ctx, RebuildUpdate{ID: state.ID, Phase: projectionDomain.PhaseAbandoned,
		LastError: "已显式放弃候选索引", Now: now})
	if err != nil {
		return RebuildState{}, err
	}
	s.observe(updated, "abandon")
	return updated, nil
}

// Cleanup 删除一个不再被引用的物理索引。
// 它要求精确目标与显式确认，并拒绝删除当前读索引、当前写索引、回滚窗口内或未知前缀的索引。
func (s *RebuildService) Cleanup(ctx context.Context, index string, confirm bool) error {
	if !confirm {
		return fmt.Errorf("%w: cleanup 必须显式确认目标索引", ErrUnsafeIndexTarget)
	}
	now := s.now().UTC()
	if !strings.HasPrefix(index, s.policy.IndexPrefix+"-v") {
		return fmt.Errorf("%w: %s 的前缀不在已知范围内", ErrUnsafeIndexTarget, index)
	}
	current, err := s.registry.State(ctx)
	if err != nil {
		return err
	}
	if index == current.CurrentIndex {
		return fmt.Errorf("%w: %s 是当前读索引", ErrUnsafeIndexTarget, index)
	}
	if current.RollbackIndex == index {
		// 回滚窗口内不得删除；窗口结束后先停止该索引的投递再删除。
		if current.RollbackDeadline == nil || now.Before(*current.RollbackDeadline) {
			return fmt.Errorf("%w: %s 仍在回滚窗口内", ErrUnsafeIndexTarget, index)
		}
		if _, err := s.store.DetachCandidate(ctx, index, now); err != nil {
			return err
		}
		if err := s.store.SetState(ctx, IndexStateRow{ReadAlias: current.ReadAlias, WriteAlias: current.WriteAlias,
			CurrentIndex: current.CurrentIndex, SchemaVersion: current.SchemaVersion,
			SchemaIdentity: current.SchemaIdentity}, now); err != nil {
			return err
		}
		// 回滚窗口已结束：把对应的重建记录收尾，避免它继续被当作活动上下文。
		if err := s.store.CompleteRebuild(ctx, current.CurrentIndex, now); err != nil {
			return err
		}
	}
	deliveries, err := s.store.IndexedDeliveries(ctx, index)
	if err != nil {
		return err
	}
	if deliveries != 0 {
		return fmt.Errorf("%w: %s 仍被 %d 条投递引用", ErrUnsafeIndexTarget, index, deliveries)
	}
	state, err := s.index.Inspect(ctx, index)
	if err != nil {
		return err
	}
	if state.HasReadAlias || state.HasWriteAlias {
		return fmt.Errorf("%w: %s 仍被别名引用", ErrUnsafeIndexTarget, index)
	}
	return s.index.Delete(ctx, index)
}

// Status 返回当前服务索引与最近的重建记录。
func (s *RebuildService) Status(ctx context.Context, limit int) (IndexStatus, error) {
	if limit < 1 || limit > 20 {
		limit = 5
	}
	status := IndexStatus{}
	if current, err := s.registry.State(ctx); err == nil {
		status.State = current
	} else if !errors.Is(err, ErrIndexNotInitialized) {
		return IndexStatus{}, err
	}
	active, err := s.store.Active(ctx)
	if err != nil {
		return IndexStatus{}, err
	}
	status.Active = active
	history, err := s.store.History(ctx, limit)
	if err != nil {
		return IndexStatus{}, err
	}
	status.History = history
	return status, nil
}

// document 补全投递所需的版本身份与内容指纹。
func (s *RebuildService) document(document Document, generation int64) Document {
	document.Generation = generation
	document.SchemaVersion = s.policy.SchemaVersion
	document.ContentHash = DocumentFingerprint(document)
	return document
}

func (s *RebuildService) fail(ctx context.Context, state RebuildState, cause error) (RebuildState, error) {
	// 阶段失败只记录诊断，不自动放弃候选：管理员可以 resume 或显式 abandon。
	// 必须沿用存储中的当前阶段：失败路径可能已经把它退回了可重试阶段。
	current, err := s.store.Get(ctx, state.ID)
	if err != nil {
		return state, cause
	}
	if _, err := s.store.Update(ctx, RebuildUpdate{ID: state.ID, Phase: current.Phase,
		LastError: cause.Error(), Now: s.now().UTC()}); err != nil {
		return state, err
	}
	return state, cause
}

func (s *RebuildService) observe(state RebuildState, action string) {
	if s.observer != nil {
		s.observer.Rebuilt(state, action)
	}
}

// Admin 把失败重试与索引重建组合成本地管理入口需要的单一服务。
// 它只暴露管理命令；Worker 使用 Executor，二者互不依赖。
type Admin struct {
	*RetryService
	*RebuildService
}

func NewAdmin(retry *RetryService, rebuild *RebuildService) *Admin {
	return &Admin{RetryService: retry, RebuildService: rebuild}
}
