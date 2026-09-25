package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
)

var (
	// ErrProjectionArticleMissing 表示要推进的文章不存在；调用方传入了错误的身份。
	ErrProjectionArticleMissing = errors.New("推进搜索投影目标时文章不存在")
	// ErrProjectionTransactionRequired 表示目标推进被要求在调用方业务事务内执行。
	// 文章与 AI 写路径必须让事实和投影目标原子提交，不能各自开事务。
	ErrProjectionTransactionRequired = errors.New("搜索投影目标推进必须位于业务事务中")
)

// SearchProjectionRepository 是搜索投影的 PostgreSQL 适配器：目标推进、任务执行与当前事实读取。
// 投影结果不会反向修改业务事实，这里也只读文章、修订与 AI 当前选择。
type SearchProjectionRepository struct{ pool *pgxpool.Pool }

func NewSearchProjectionRepository(pool *pgxpool.Pool) *SearchProjectionRepository {
	return &SearchProjectionRepository{pool: pool}
}

var _ projectionApp.TargetStore = (*SearchProjectionRepository)(nil)

// activeIndexesSQL 是合法的投递目标集合：当前服务索引、回滚窗口内的前一索引，
// 以及仍在活动阶段的重建候选索引。它必须与 delivery 索引守卫触发器保持一致。
const activeIndexesSQL = `SELECT current_index FROM velis.search_index_state
UNION SELECT rollback_index FROM velis.search_index_state WHERE rollback_index IS NOT NULL
UNION SELECT candidate_index FROM velis.search_index_rebuilds
WHERE phase IN ('snapshot', 'catchup', 'validate', 'validated', 'cutover', 'serving')`

// Advance 推进目标；已处于事务内时加入调用方事务，否则使用一个短事务。
// 文章写路径与 AI 结果事务必须使用前一种形式，使事实与投影目标原子提交。
func (r *SearchProjectionRepository) Advance(ctx context.Context, articleID int64, now time.Time) (bool, error) {
	if tx, ok := transactionFromContext(ctx); ok {
		return advanceSearchProjectionTarget(ctx, tx, articleID, now)
	}
	var changed bool
	err := NewTxManager(r.pool).WithinTransaction(ctx, func(txContext context.Context) error {
		tx, _ := transactionFromContext(txContext)
		var err error
		changed, err = advanceSearchProjectionTarget(txContext, tx, articleID, now)
		return err
	})
	return changed, err
}

// advanceSearchProjectionTarget 从已锁定的当前事实构造完整目标并推进该文章唯一的槽位。
// 调用方不自行拼目标版本：公开状态决定 upsert，草稿、下架与删除决定 tombstone。
func advanceSearchProjectionTarget(ctx context.Context, tx pgx.Tx, articleID int64, now time.Time) (bool, error) {
	var status string
	var lockVersion, revisionID int64
	var generationResultID, embeddingResultID *string
	err := tx.QueryRow(ctx, `SELECT a.status::text,a.lock_version,a.current_revision_id,
s.generation_result_id::text,s.embedding_result_id::text
FROM velis.articles a
LEFT JOIN velis.ai_current_selections s ON s.article_id=a.id AND s.revision_id=a.current_revision_id
WHERE a.id=$1 FOR UPDATE OF a`, articleID).Scan(&status, &lockVersion, &revisionID, &generationResultID, &embeddingResultID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrProjectionArticleMissing
	}
	if err != nil {
		return false, err
	}
	// 下架不会删除 AI 当前选择，但 tombstone 目标只携带身份与版本，因此必须显式清空结果引用。
	action := projectionDomain.ActionTombstone
	var generation, embedding *string
	if status == "published" {
		action = projectionDomain.ActionUpsert
		generation, embedding = generationResultID, embeddingResultID
	}
	target, err := projectionDomain.NewTarget(action, lockVersion, revisionID, generation, embedding)
	if err != nil {
		return false, err
	}

	current, err := lockProjectionJob(ctx, tx, articleID)
	if err != nil {
		return false, err
	}
	// 目标完全相同时必须不取新序列、不增加 generation：这是「重复推进为 noop」的判定点。
	if current != nil && current.Target.Equal(target) {
		return false, nil
	}
	var changeSeq int64
	if err := tx.QueryRow(ctx, `SELECT nextval('velis.search_projection_change_seq')`).Scan(&changeSeq); err != nil {
		return false, err
	}
	job, _, err := projectionDomain.Advance(articleID, current, target, changeSeq, now.UTC())
	if err != nil {
		return false, err
	}
	if err := upsertProjectionJob(ctx, tx, job, now.UTC()); err != nil {
		return false, err
	}
	if err := resetActiveDeliveries(ctx, tx, articleID, job.Generation, now.UTC()); err != nil {
		return false, err
	}
	return true, nil
}

func lockProjectionJob(ctx context.Context, tx pgx.Tx, articleID int64) (*projectionDomain.Job, error) {
	var job projectionDomain.Job
	var action, status string
	var generationResultID, embeddingResultID *string
	var targetChangedAt, createdAt time.Time
	var completedAt *time.Time
	err := tx.QueryRow(ctx, `SELECT generation,change_seq,action::text,article_lock_version,revision_id,
generation_result_id::text,embedding_result_id::text,status::text,attempt,target_changed_at,completed_at,created_at
FROM velis.search_projection_jobs WHERE article_id=$1 FOR UPDATE`, articleID).Scan(
		&job.Generation, &job.ChangeSeq, &action, &job.Target.ArticleLockVersion, &job.Target.RevisionID,
		&generationResultID, &embeddingResultID, &status, &job.Attempt, &targetChangedAt, &completedAt, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	target, err := projectionDomain.NewTarget(projectionDomain.Action(action), job.Target.ArticleLockVersion,
		job.Target.RevisionID, generationResultID, embeddingResultID)
	if err != nil {
		return nil, err
	}
	job.ArticleID = articleID
	job.Target = target
	job.Status = projectionDomain.Status(status)
	job.TargetChangedAt = targetChangedAt.UTC()
	job.CompletedAt = utcTimePointer(completedAt)
	job.CreatedAt = createdAt.UTC()
	return &job, nil
}

func upsertProjectionJob(ctx context.Context, tx pgx.Tx, job projectionDomain.Job, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO velis.search_projection_jobs
(article_id,action,generation,change_seq,article_lock_version,revision_id,generation_result_id,embedding_result_id,
status,attempt,next_attempt_at,target_changed_at,created_at,updated_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,'pending',0,$9,$9,$9,$9)
ON CONFLICT (article_id) DO UPDATE SET
action=EXCLUDED.action,generation=EXCLUDED.generation,change_seq=EXCLUDED.change_seq,
article_lock_version=EXCLUDED.article_lock_version,revision_id=EXCLUDED.revision_id,
generation_result_id=EXCLUDED.generation_result_id,embedding_result_id=EXCLUDED.embedding_result_id,
status='pending',attempt=0,next_attempt_at=EXCLUDED.next_attempt_at,
lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,
last_error_code=NULL,last_error_message=NULL,
target_changed_at=EXCLUDED.target_changed_at,completed_at=NULL,updated_at=EXCLUDED.updated_at`,
		job.ArticleID, string(job.Target.Action), job.Generation, job.ChangeSeq, job.Target.ArticleLockVersion,
		job.Target.RevisionID, job.Target.GenerationResultID, job.Target.EmbeddingResultID, now)
	return err
}

// resetActiveDeliveries 让全部 delivery 指向新 generation 并回到待处理，同时为活动索引补齐缺失的 delivery。
// 补齐让「索引先于槽位建立」与「槽位先于索引建立」两种顺序都能收敛。
func resetActiveDeliveries(ctx context.Context, tx pgx.Tx, articleID, generation int64, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE velis.search_projection_deliveries
SET required_generation=$2,status='pending',attempt=0,next_attempt_at=$3,
last_result=NULL,last_error_code=NULL,last_error_message=NULL,updated_at=$3
WHERE article_id=$1`, articleID, generation, now); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO velis.search_projection_deliveries
(article_id,physical_index,required_generation,status,next_attempt_at,updated_at)
SELECT $1, active.index, $2, 'pending', $3, $3 FROM (`+activeIndexesSQL+`) AS active(index)
ON CONFLICT (article_id, physical_index) DO NOTHING`, articleID, generation, now)
	return err
}

// ensureActiveDeliveries 为已有槽位补齐活动索引的 delivery；索引初始化与重建注册后调用。
func (r *SearchProjectionRepository) ensureActiveDeliveries(ctx context.Context, now time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `INSERT INTO velis.search_projection_deliveries
(article_id,physical_index,required_generation,status,next_attempt_at,updated_at)
SELECT j.article_id, active.index, j.generation, 'pending', $1, $1
FROM velis.search_projection_jobs j CROSS JOIN (`+activeIndexesSQL+`) AS active(index)
ON CONFLICT (article_id, physical_index) DO NOTHING`, now.UTC())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}
