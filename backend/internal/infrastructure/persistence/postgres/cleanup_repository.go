package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CleanupRepository 按保留期分批清理会话、令牌与限流状态；用户和审计不参与清理。
type CleanupRepository struct{ pool *pgxpool.Pool }

func NewCleanupRepository(pool *pgxpool.Pool) *CleanupRepository {
	return &CleanupRepository{pool: pool}
}

func (r *CleanupRepository) DeleteExpiredSessions(ctx context.Context, before time.Time, limit int) (int64, error) {
	return r.deleteBatch(ctx, `DELETE FROM velis.auth_sessions WHERE id IN (
SELECT id FROM velis.auth_sessions WHERE COALESCE(revoked_at, expires_at) < $1 LIMIT $2)`, before, limit)
}

func (r *CleanupRepository) DeleteExpiredRefreshTokens(ctx context.Context, before time.Time, limit int) (int64, error) {
	return r.deleteBatch(ctx, `DELETE FROM velis.refresh_tokens WHERE id IN (
SELECT id FROM velis.refresh_tokens WHERE expires_at < $1 LIMIT $2)`, before, limit)
}

func (r *CleanupRepository) DeleteStaleFailures(ctx context.Context, before time.Time, limit int) (int64, error) {
	return r.deleteBatch(ctx, `DELETE FROM velis.login_failure_events WHERE id IN (
SELECT id FROM velis.login_failure_events WHERE occurred_at < $1 LIMIT $2)`, before, limit)
}

func (r *CleanupRepository) DeleteExpiredBlocks(ctx context.Context, before time.Time, limit int) (int64, error) {
	return r.deleteBatch(ctx, `DELETE FROM velis.login_blocks WHERE ctid IN (
SELECT ctid FROM velis.login_blocks WHERE blocked_until < $1 LIMIT $2)`, before, limit)
}

func (r *CleanupRepository) deleteBatch(ctx context.Context, statement string, before time.Time, limit int) (int64, error) {
	tag, err := querier(ctx, r.pool).Exec(ctx, statement, before, limit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
