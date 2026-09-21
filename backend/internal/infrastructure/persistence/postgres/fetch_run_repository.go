package postgres

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
)

// FetchRunRepository 持久化抓取历史。表结构只容纳计数与受控错误码，
// 完整响应正文、代理凭据与请求头没有可用列，因此不可能被写入。
type FetchRunRepository struct{ pool *pgxpool.Pool }

var _ sourceDomain.FetchRunRepository = (*FetchRunRepository)(nil)

func NewFetchRunRepository(pool *pgxpool.Pool) *FetchRunRepository {
	return &FetchRunRepository{pool: pool}
}

const fetchRunColumns = `id::text, source_id, trigger, actor_user_id::text, status, lease_generation,
not_modified, inserted_count, updated_count, unchanged_count, skipped_count, error_code,
started_at, completed_at, created_at`

func (r *FetchRunRepository) executor(ctx context.Context) commandExecutor {
	if tx, ok := transactionFromContext(ctx); ok {
		return tx
	}
	return r.pool
}

// Start 记录开始运行的运行；同一来源已有运行中的记录时被唯一索引拒绝。
func (r *FetchRunRepository) Start(ctx context.Context, run sourceDomain.FetchRun) error {
	_, err := r.executor(ctx).Exec(ctx, `INSERT INTO velis.source_fetch_runs
(id,source_id,trigger,actor_user_id,status,lease_generation,started_at,created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, run.ID, run.SourceID, run.Trigger, run.ActorUserID,
		run.Status, run.LeaseGeneration, run.StartedAt, run.CreatedAt)
	return err
}

// Complete 写入终态与统计。lease_generation 必须与写入时来源上的 generation 一致，
// 否则说明租约已被其他认领取代，本次完成不生效。
func (r *FetchRunRepository) Complete(ctx context.Context, run sourceDomain.FetchRun) error {
	tag, err := r.executor(ctx).Exec(ctx, `UPDATE velis.source_fetch_runs SET
status=$3,not_modified=$4,inserted_count=$5,updated_count=$6,unchanged_count=$7,skipped_count=$8,
error_code=$9,completed_at=$10
WHERE id=$1 AND source_id=$2 AND status='running'
AND lease_generation = (SELECT lease_generation FROM velis.sources WHERE id=$2)`,
		run.ID, run.SourceID, run.Status, run.NotModified, run.Inserted, run.Updated, run.Unchanged,
		run.Skipped, run.ErrorCode, run.CompletedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return sourceDomain.ErrLeaseLost
	}
	return nil
}

// AbortStale 收敛租约已被取代的运行，使其不再永久停留在运行中。
func (r *FetchRunRepository) AbortStale(ctx context.Context, sourceID, currentGeneration int64, now time.Time) (int64, error) {
	tag, err := r.executor(ctx).Exec(ctx, `UPDATE velis.source_fetch_runs SET status='aborted',completed_at=$3
WHERE source_id=$1 AND status='running' AND lease_generation < $2`, sourceID, currentGeneration, now)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ListBySource 按开始时间倒序翻页；同一毫秒用运行 ID 兜底，保证翻页无重复。
func (r *FetchRunRepository) ListBySource(ctx context.Context, sourceID int64, cursor *sourceDomain.FetchRunCursor, limit int) ([]sourceDomain.FetchRun, error) {
	query := `SELECT ` + fetchRunColumns + ` FROM velis.source_fetch_runs WHERE source_id=$1`
	args := []any{sourceID}
	if cursor != nil {
		query += ` AND (started_at, id) < ($2, $3)`
		args = append(args, cursor.StartedAt, cursor.ID)
	}
	query += ` ORDER BY started_at DESC, id DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit)
	rows, err := querier(ctx, r.pool).Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := make([]sourceDomain.FetchRun, 0)
	for rows.Next() {
		run, scanErr := scanFetchRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// CurrentRunning 返回来源上仍在运行的记录；唯一索引保证最多一条。
func (r *FetchRunRepository) CurrentRunning(ctx context.Context, sourceID int64) (sourceDomain.FetchRun, error) {
	return scanFetchRun(querier(ctx, r.pool).QueryRow(ctx,
		`SELECT `+fetchRunColumns+` FROM velis.source_fetch_runs WHERE source_id=$1 AND status='running'`, sourceID))
}

func scanFetchRun(row scanner) (sourceDomain.FetchRun, error) {
	var run sourceDomain.FetchRun
	var trigger, status string
	var stats sourceDomain.FetchRunStats
	err := row.Scan(&run.ID, &run.SourceID, &trigger, &run.ActorUserID, &status, &run.LeaseGeneration,
		&stats.NotModified, &stats.Inserted, &stats.Updated, &stats.Unchanged, &stats.Skipped,
		&run.ErrorCode, &run.StartedAt, &run.CompletedAt, &run.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return sourceDomain.FetchRun{}, sourceDomain.ErrNotFound
	}
	if err != nil {
		return sourceDomain.FetchRun{}, err
	}
	return sourceDomain.Restore(run.ID, run.SourceID, sourceDomain.FetchTrigger(trigger), run.ActorUserID,
		sourceDomain.FetchRunStatus(status), run.LeaseGeneration, stats, run.ErrorCode,
		run.StartedAt, run.CompletedAt, run.CreatedAt), nil
}
