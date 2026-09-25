package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
)

var (
	_ projectionApp.RebuildStore  = (*SearchProjectionRepository)(nil)
	_ projectionApp.IndexRegistry = (*SearchProjectionRepository)(nil)
)

// activeRebuildPhases 是占用「同一逻辑索引只允许一个活动重建」配额且尚未切换的阶段。
const activeRebuildPhases = `('snapshot','catchup','validate','validated','cutover')`

// settleJobsSQL 在 delivery 集合变化后重新派生槽位状态。
// 没有 delivery 时保持待处理：没有索引可写不等于投影已经完成。
const settleJobsSQL = `UPDATE velis.search_projection_jobs j
SET status=derived.status,completed_at=derived.completed_at,next_attempt_at=$1,updated_at=$1
FROM (
    SELECT j2.article_id,
        CASE WHEN c.converged THEN 'succeeded' ELSE 'pending' END AS status,
        CASE WHEN c.converged THEN $1::timestamptz ELSE NULL END AS completed_at
    FROM velis.search_projection_jobs j2
    CROSS JOIN LATERAL (
        SELECT count(*)>0 AND bool_and(d.required_generation=j2.generation AND d.status='succeeded') AS converged
        FROM velis.search_projection_deliveries d WHERE d.article_id=j2.article_id
    ) c
) derived
WHERE j.article_id=derived.article_id AND j.lease_token IS NULL`

// Begin 在一个事务内抢占重建配额、记录 start change sequence 并建立候选索引记录。
func (r *SearchProjectionRepository) Begin(ctx context.Context, start projectionApp.RebuildStart) (projectionApp.RebuildState, error) {
	now := start.Now.UTC()
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return projectionApp.RebuildState{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// 单行索引状态加锁：重建开始必须串行，避免两个候选同时注册。
	var current string
	err = tx.QueryRow(ctx, `SELECT current_index FROM velis.search_index_state WHERE id FOR UPDATE`).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return projectionApp.RebuildState{}, projectionApp.ErrIndexNotInitialized
	}
	if err != nil {
		return projectionApp.RebuildState{}, err
	}
	if current == start.CandidateIndex {
		return projectionApp.RebuildState{}, fmt.Errorf("%w: 候选索引与当前服务索引相同", projectionApp.ErrRebuildConflict)
	}
	var conflict bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM velis.search_index_rebuilds WHERE phase IN `+activeRebuildPhases+`)
OR EXISTS(SELECT 1 FROM velis.search_index_state WHERE rollback_index IS NOT NULL)`).Scan(&conflict); err != nil {
		return projectionApp.RebuildState{}, err
	}
	if conflict {
		return projectionApp.RebuildState{}, projectionApp.ErrRebuildConflict
	}
	// start_change_seq 取 nextval：任何更早分配的目标变化都已经或即将可见，
	// 而更晚分配的一定落在增量追赶范围内。
	var startSeq int64
	if err := tx.QueryRow(ctx, `SELECT nextval('velis.search_projection_change_seq')`).Scan(&startSeq); err != nil {
		return projectionApp.RebuildState{}, err
	}
	state, err := scanRebuildState(tx.QueryRow(ctx, `INSERT INTO velis.search_index_rebuilds
(id,candidate_index,target_schema_version,schema_identity,phase,start_change_seq,started_at,updated_at)
VALUES($1,$2,$3,$4,'snapshot',$5,$6,$6)
RETURNING `+rebuildColumns, uuid.NewString(), start.CandidateIndex, start.TargetSchemaVersion,
		start.SchemaIdentity, startSeq, now))
	if err != nil {
		return projectionApp.RebuildState{}, err
	}
	return state, tx.Commit(ctx)
}

const rebuildColumns = `id::text,candidate_index,target_schema_version,schema_identity,phase::text,
start_change_seq,snapshot_watermark,snapshot_documents,catchup_watermark,validation_report,
validated_at,cutover_at,rollback_deadline,last_error,started_at,updated_at`

func scanRebuildState(row pgx.Row) (projectionApp.RebuildState, error) {
	var state projectionApp.RebuildState
	var phase string
	var report []byte
	var lastError *string
	err := row.Scan(&state.ID, &state.CandidateIndex, &state.TargetSchemaVersion, &state.SchemaIdentity, &phase,
		&state.StartChangeSeq, &state.SnapshotWatermark, &state.SnapshotDocuments, &state.CatchUpWatermark,
		&report, &state.ValidatedAt, &state.CutoverAt, &state.RollbackDeadline, &lastError,
		&state.StartedAt, &state.UpdatedAt)
	if err != nil {
		return projectionApp.RebuildState{}, err
	}
	if lastError != nil {
		state.LastError = *lastError
	}
	state.Phase = projectionDomain.Phase(phase)
	if len(report) > 0 {
		var validation projectionDomain.ValidationReport
		if err := json.Unmarshal(report, &validation); err != nil {
			return projectionApp.RebuildState{}, fmt.Errorf("解析校验报告: %w", err)
		}
		state.Validation = &validation
	}
	return state, nil
}

func (r *SearchProjectionRepository) rebuildByQuery(ctx context.Context, query string, args ...any) (projectionApp.RebuildState, error) {
	state, err := scanRebuildState(r.pool.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return projectionApp.RebuildState{}, projectionApp.ErrRebuildNotFound
	}
	return state, err
}

// Active 返回仍在生命周期内的最近一条重建记录。
// serving 同样属于生命周期内：回滚窗口未关闭前 rollback 必须能找到它。
func (r *SearchProjectionRepository) Active(ctx context.Context) (*projectionApp.RebuildState, error) {
	state, err := r.rebuildByQuery(ctx, `SELECT `+rebuildColumns+` FROM velis.search_index_rebuilds
WHERE phase NOT IN ('completed','abandoned','failed') ORDER BY started_at DESC LIMIT 1`)
	if errors.Is(err, projectionApp.ErrRebuildNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &state, nil
}

// Get 按 ID 读取重建记录。
func (r *SearchProjectionRepository) Get(ctx context.Context, id string) (projectionApp.RebuildState, error) {
	return r.rebuildByQuery(ctx, `SELECT `+rebuildColumns+` FROM velis.search_index_rebuilds WHERE id=$1::uuid`, id)
}

// History 按开始时间倒序返回最近记录。
func (r *SearchProjectionRepository) History(ctx context.Context, limit int) ([]projectionApp.RebuildState, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+rebuildColumns+` FROM velis.search_index_rebuilds
ORDER BY started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := make([]projectionApp.RebuildState, 0, limit)
	for rows.Next() {
		state, err := scanRebuildState(rows)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, rows.Err()
}

// Update 按状态机持久化阶段与水位；非法转换返回 ErrInvalidRebuildPhase。
func (r *SearchProjectionRepository) Update(ctx context.Context, update projectionApp.RebuildUpdate) (projectionApp.RebuildState, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return projectionApp.RebuildState{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanRebuildState(tx.QueryRow(ctx, `SELECT `+rebuildColumns+
		` FROM velis.search_index_rebuilds WHERE id=$1::uuid FOR UPDATE`, update.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		return projectionApp.RebuildState{}, projectionApp.ErrRebuildNotFound
	}
	if err != nil {
		return projectionApp.RebuildState{}, err
	}
	if !current.Phase.CanTransition(update.Phase) {
		return projectionApp.RebuildState{}, fmt.Errorf("%w: %s → %s", projectionApp.ErrInvalidRebuildPhase, current.Phase, update.Phase)
	}
	var report []byte
	if update.Validation != nil {
		report, err = json.Marshal(update.Validation)
		if err != nil {
			return projectionApp.RebuildState{}, err
		}
	}
	now := update.Now.UTC()
	state, err := scanRebuildState(tx.QueryRow(ctx, `UPDATE velis.search_index_rebuilds SET
phase=$2::varchar,snapshot_watermark=GREATEST(snapshot_watermark,$3),snapshot_documents=GREATEST(snapshot_documents,$4),
catchup_watermark=GREATEST(catchup_watermark,$5),
validation_report=COALESCE($6::jsonb,validation_report),validated_at=CASE WHEN $2::varchar='validated' THEN $7 ELSE validated_at END,
cutover_at=COALESCE($9,cutover_at),rollback_deadline=COALESCE($10,rollback_deadline),
last_error=$8,updated_at=$7
WHERE id=$1::uuid RETURNING `+rebuildColumns, update.ID, string(update.Phase), update.SnapshotWatermark,
		update.SnapshotDocuments, update.CatchUpWatermark, report, now, update.LastError,
		update.CutoverAt, update.RollbackDeadline))
	if err != nil {
		return projectionApp.RebuildState{}, err
	}
	return state, tx.Commit(ctx)
}

// lockAllJobs 按 article_id 顺序锁定全部槽位。
//
// 全表加锁顺序必须与文章写路径保持一致：article → job → delivery。
// 反过来先锁 delivery 再锁 job 会与并发文章修订形成 ABBA 死锁。
func lockAllJobs(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT article_id FROM velis.search_projection_jobs ORDER BY article_id FOR UPDATE`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var articleID int64
		if err := rows.Scan(&articleID); err != nil {
			return err
		}
	}
	return rows.Err()
}

// RegisterCandidate 把候选索引注册为全部槽位的第二活动 delivery，并重新打开因此落后的槽位。
func (r *SearchProjectionRepository) RegisterCandidate(ctx context.Context, index string, now time.Time) (int64, error) {
	now = now.UTC()
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAllJobs(ctx, tx); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO velis.search_projection_deliveries
(article_id,physical_index,required_generation,status,next_attempt_at,updated_at)
SELECT j.article_id,$1,j.generation,'pending',$2,$2 FROM velis.search_projection_jobs j
ON CONFLICT (article_id,physical_index) DO UPDATE SET
required_generation=EXCLUDED.required_generation,status='pending',attempt=0,
next_attempt_at=EXCLUDED.next_attempt_at,last_result=NULL,last_error_code=NULL,last_error_message=NULL,
updated_at=EXCLUDED.updated_at`, index, now)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, settleJobsSQL, now); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}

// DetachCandidate 解除候选索引的全部 delivery，并重新派生不再落后的槽位状态。
func (r *SearchProjectionRepository) DetachCandidate(ctx context.Context, index string, now time.Time) (int64, error) {
	now = now.UTC()
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAllJobs(ctx, tx); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM velis.search_projection_deliveries WHERE physical_index=$1`, index)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, settleJobsSQL, now); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}

// CloseRollbackWindow 结束回滚窗口：解除前一索引的 delivery 并清空回滚状态。
func (r *SearchProjectionRepository) CloseRollbackWindow(ctx context.Context, now time.Time) error {
	now = now.UTC()
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var rollback string
	err = tx.QueryRow(ctx, `SELECT COALESCE(rollback_index,'') FROM velis.search_index_state WHERE id FOR UPDATE`).Scan(&rollback)
	if errors.Is(err, pgx.ErrNoRows) {
		return projectionApp.ErrIndexNotInitialized
	}
	if err != nil {
		return err
	}
	// 先锁槽位再动 delivery，避免与并发文章修订形成锁顺序反转。
	if err := lockAllJobs(ctx, tx); err != nil {
		return err
	}
	if rollback != "" {
		if _, err := tx.Exec(ctx, `DELETE FROM velis.search_projection_deliveries WHERE physical_index=$1`, rollback); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE velis.search_index_state
SET rollback_index=NULL,rollback_deadline=NULL,updated_at=$1 WHERE id`, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, settleJobsSQL, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// State 返回当前索引服务状态；尚未初始化时返回 ErrIndexNotInitialized。
func (r *SearchProjectionRepository) State(ctx context.Context) (projectionApp.IndexStateRow, error) {
	var row projectionApp.IndexStateRow
	var rollback *string
	err := r.pool.QueryRow(ctx, `SELECT read_alias,write_alias,current_index,rollback_index,
rollback_deadline,schema_version,schema_identity,updated_at FROM velis.search_index_state WHERE id`).
		Scan(&row.ReadAlias, &row.WriteAlias, &row.CurrentIndex, &rollback, &row.RollbackDeadline,
			&row.SchemaVersion, &row.SchemaIdentity, &row.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return projectionApp.IndexStateRow{}, projectionApp.ErrIndexNotInitialized
	}
	if err != nil {
		return projectionApp.IndexStateRow{}, err
	}
	if rollback != nil {
		row.RollbackIndex = *rollback
	}
	return row, nil
}

// EnsureSlots 为尚未建立槽位的文章创建待处理目标，并返回各文章的当前 generation。
// 快照必须用它作为文档外部版本，否则后续更低 generation 的写入会被版本条件拒绝。
func (r *SearchProjectionRepository) EnsureSlots(ctx context.Context, articleIDs []int64, now time.Time) ([]projectionApp.ArticleGeneration, error) {
	if len(articleIDs) == 0 {
		return nil, nil
	}
	now = now.UTC()
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, articleID := range articleIDs {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM velis.search_projection_jobs WHERE article_id=$1)`, articleID).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			continue
		}
		if _, err := advanceSearchProjectionTarget(ctx, tx, articleID, now); err != nil {
			return nil, err
		}
	}
	rows, err := tx.Query(ctx, `SELECT article_id,generation FROM velis.search_projection_jobs
WHERE article_id = ANY($1::bigint[]) ORDER BY article_id`, articleIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	generations := make([]projectionApp.ArticleGeneration, 0, len(articleIDs))
	for rows.Next() {
		var item projectionApp.ArticleGeneration
		if err := rows.Scan(&item.ArticleID, &item.Generation); err != nil {
			return nil, err
		}
		generations = append(generations, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return generations, tx.Commit(ctx)
}

// SetState 持久化当前读写别名、当前服务索引与回滚窗口。
// 别名切换与状态写入必须成对出现：状态只会描述已经生效的索引。
func (r *SearchProjectionRepository) SetState(ctx context.Context, row projectionApp.IndexStateRow, now time.Time) error {
	var rollback any
	var deadline any
	if row.RollbackIndex != "" {
		rollback = row.RollbackIndex
	}
	if row.RollbackDeadline != nil {
		deadline = row.RollbackDeadline.UTC()
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO velis.search_index_state
(id,read_alias,write_alias,current_index,rollback_index,rollback_deadline,schema_version,schema_identity,updated_at)
VALUES(true,$1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (id) DO UPDATE SET read_alias=EXCLUDED.read_alias,write_alias=EXCLUDED.write_alias,
current_index=EXCLUDED.current_index,rollback_index=EXCLUDED.rollback_index,
rollback_deadline=EXCLUDED.rollback_deadline,schema_version=EXCLUDED.schema_version,
schema_identity=EXCLUDED.schema_identity,updated_at=EXCLUDED.updated_at`,
		row.ReadAlias, row.WriteAlias, row.CurrentIndex, rollback, deadline, row.SchemaVersion, row.SchemaIdentity, now.UTC())
	return err
}

// ConvergeCandidate 由 CLI 在候选索引上直接推进一个 delivery。
// 只有槽位仍处于该 generation 时才会生效：若目标已推进，本次投递由后续增量扫描覆盖。
func (r *SearchProjectionRepository) ConvergeCandidate(ctx context.Context, articleID int64, index string, generation int64, result string, now time.Time) (bool, error) {
	now = now.UTC()
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// 先锁 job 行：文章写路径的顺序是 article → job → delivery，这里必须保持一致。
	var locked int64
	err = tx.QueryRow(ctx, `SELECT generation FROM velis.search_projection_jobs WHERE article_id=$1 FOR UPDATE`, articleID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && locked != generation) {
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}
	command, err := tx.Exec(ctx, `UPDATE velis.search_projection_deliveries d
SET status='succeeded',last_result=$4,last_error_code=NULL,last_error_message=NULL,updated_at=$5
FROM velis.search_projection_jobs j
WHERE d.article_id=$1 AND d.physical_index=$2 AND d.article_id=j.article_id
  AND j.generation=$3 AND d.required_generation=$3 AND d.status<>'succeeded'`,
		articleID, index, generation, result, now)
	if err != nil {
		return false, err
	}
	if command.RowsAffected() == 0 {
		return false, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, settleJobsSQL, now); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// EnsureActiveDeliveries 为已有槽位补齐当前活动索引的 delivery；索引初始化后必须调用。
func (r *SearchProjectionRepository) EnsureActiveDeliveries(ctx context.Context, now time.Time) (int64, error) {
	return r.ensureActiveDeliveries(ctx, now.UTC())
}

// LaggingCandidateDeliveries 统计某物理索引上尚未达到槽位当前 generation 的投递数量。
func (r *SearchProjectionRepository) LaggingCandidateDeliveries(ctx context.Context, index string) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM velis.search_projection_deliveries d
JOIN velis.search_projection_jobs j ON j.article_id=d.article_id
WHERE d.physical_index=$1 AND (d.required_generation<>j.generation OR d.status<>'succeeded')`, index).Scan(&count)
	return count, err
}

// CompleteRebuild 把指定候选索引上仍处于 serving 的重建记录标记为 completed。
func (r *SearchProjectionRepository) CompleteRebuild(ctx context.Context, index string, now time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE velis.search_index_rebuilds SET phase='completed',updated_at=$2
WHERE candidate_index=$1 AND phase='serving'`, index, now.UTC())
	return err
}

// ActiveLeases 统计仍在执行且租约未过期的槽位数量。
func (r *SearchProjectionRepository) ActiveLeases(ctx context.Context, now time.Time) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM velis.search_projection_jobs
WHERE status='running' AND lease_expires_at > $1`, now.UTC()).Scan(&count)
	return count, err
}

// PendingCandidateDeliveries 按文章 ID 升序返回候选索引上仍待处理的槽位。
func (r *SearchProjectionRepository) PendingCandidateDeliveries(ctx context.Context, index string, limit int) ([]projectionApp.ArticleGeneration, error) {
	rows, err := r.pool.Query(ctx, `SELECT j.article_id,j.generation FROM velis.search_projection_deliveries d
JOIN velis.search_projection_jobs j ON j.article_id=d.article_id
WHERE d.physical_index=$1 AND (d.required_generation<>j.generation OR d.status<>'succeeded')
ORDER BY j.article_id LIMIT $2`, index, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]projectionApp.ArticleGeneration, 0, limit)
	for rows.Next() {
		var item projectionApp.ArticleGeneration
		if err := rows.Scan(&item.ArticleID, &item.Generation); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// IndexedDeliveries 统计某物理索引上被引用的 delivery 数量，供 cleanup 安全判断。
func (r *SearchProjectionRepository) IndexedDeliveries(ctx context.Context, index string) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM velis.search_projection_deliveries WHERE physical_index=$1`, index).Scan(&count)
	return count, err
}
