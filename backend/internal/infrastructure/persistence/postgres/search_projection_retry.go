package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
)

var _ projectionApp.RetryStore = (*SearchProjectionRepository)(nil)

// Reactivate 只重新激活仍处于槽位当前 generation 的失败 delivery。
//
// 三个条件在同一条语句内求值，因此即使读取后目标立刻被推进，也不会把已推进的新
// generation 拉回旧失败：generation 不匹配或状态已不是 failed 的投递不会被匹配。
func (r *SearchProjectionRepository) Reactivate(ctx context.Context, scope projectionApp.RetryScope, now time.Time) (projectionApp.RetryReport, error) {
	now = now.UTC()
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return projectionApp.RetryReport{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	report := projectionApp.RetryReport{Scope: scope, RunAt: now}
	if err := tx.QueryRow(ctx, `SELECT
count(*) FILTER (WHERE d.status='failed' AND d.required_generation=j.generation),
count(*) FILTER (WHERE d.status='failed' AND d.required_generation<>j.generation),
count(*) FILTER (WHERE d.status IN ('pending','running','retry_wait'))
FROM velis.search_projection_deliveries d
JOIN velis.search_projection_jobs j ON j.article_id=d.article_id
WHERE ($1::bigint=0 OR d.article_id=$1) AND ($2::text='' OR d.physical_index=$2)`,
		scope.ArticleID, scope.Index).Scan(&report.FailedTotal, &report.Superseded, &report.Remaining); err != nil {
		return projectionApp.RetryReport{}, err
	}

	rows, err := tx.Query(ctx, `WITH target AS (
    SELECT d.article_id,d.physical_index FROM velis.search_projection_deliveries d
    JOIN velis.search_projection_jobs j ON j.article_id=d.article_id
    WHERE d.status='failed' AND d.required_generation=j.generation
      AND ($1::bigint=0 OR d.article_id=$1) AND ($2::text='' OR d.physical_index=$2)
    ORDER BY d.updated_at,d.article_id,d.physical_index
    FOR UPDATE OF d SKIP LOCKED LIMIT $3
), updated AS (
    UPDATE velis.search_projection_deliveries d
    SET status='pending',attempt=0,next_attempt_at=$4,
        last_error_code=NULL,last_error_message=NULL,updated_at=$4
    FROM target t WHERE d.article_id=t.article_id AND d.physical_index=t.physical_index
      AND d.status='failed'
    RETURNING d.article_id,d.physical_index,d.required_generation
), reopened AS (
    UPDATE velis.search_projection_jobs j
    SET status='pending',next_attempt_at=$4,completed_at=NULL,
        last_error_code=NULL,last_error_message=NULL,updated_at=$4
    FROM updated u WHERE j.article_id=u.article_id AND j.status='failed' AND j.lease_token IS NULL
    RETURNING j.article_id
)
SELECT article_id,physical_index,required_generation FROM updated ORDER BY article_id,physical_index`,
		scope.ArticleID, scope.Index, scope.Limit, now)
	if err != nil {
		return projectionApp.RetryReport{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item projectionApp.RetriedDelivery
		if err := rows.Scan(&item.ArticleID, &item.Index, &item.Generation); err != nil {
			return projectionApp.RetryReport{}, err
		}
		report.Reactivated = append(report.Reactivated, item)
	}
	if err := rows.Err(); err != nil {
		return projectionApp.RetryReport{}, err
	}
	if len(report.Reactivated) == 0 {
		report.Reactivated = []projectionApp.RetriedDelivery{}
	}
	if err := tx.Commit(ctx); err != nil {
		return projectionApp.RetryReport{}, err
	}
	return report, nil
}
