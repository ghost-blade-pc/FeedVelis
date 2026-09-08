package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
)

type SourceRepository struct{ pool *pgxpool.Pool }

func NewSourceRepository(pool *pgxpool.Pool) *SourceRepository { return &SourceRepository{pool: pool} }

const sourceColumns = `id, feed_url, normalized_feed_url, site_url, title, status, etag, last_modified,
next_fetch_at, last_checked_at, last_success_at, consecutive_failures, last_error_code,
lease_owner, lease_expires_at, created_at, updated_at`

const returnedSourceColumns = `s.id, s.feed_url, s.normalized_feed_url, s.site_url, s.title, s.status, s.etag, s.last_modified,
s.next_fetch_at, s.last_checked_at, s.last_success_at, s.consecutive_failures, s.last_error_code,
s.lease_owner, s.lease_expires_at, s.created_at, s.updated_at`

func (r *SourceRepository) Add(ctx context.Context, rawURL, normalizedURL, title string, now time.Time) (sourceDomain.Source, bool, error) {
	row := r.pool.QueryRow(ctx, `INSERT INTO velis.sources
(feed_url, normalized_feed_url, title, status, next_fetch_at, created_at, updated_at)
VALUES ($1, $2, $3, 'active', $4, $4, $4)
ON CONFLICT (normalized_feed_url) DO NOTHING RETURNING `+sourceColumns, rawURL, normalizedURL, title, now)
	src, err := scanSource(row)
	if err == nil {
		return src, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sourceDomain.Source{}, false, err
	}
	src, err = scanSource(r.pool.QueryRow(ctx, `SELECT `+sourceColumns+` FROM velis.sources WHERE normalized_feed_url=$1`, normalizedURL))
	return src, false, err
}

func (r *SourceRepository) List(ctx context.Context) ([]sourceDomain.Source, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+sourceColumns+` FROM velis.sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]sourceDomain.Source, 0)
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, src)
	}
	return items, rows.Err()
}

func (r *SourceRepository) Get(ctx context.Context, id int64) (sourceDomain.Source, error) {
	src, err := scanSource(r.pool.QueryRow(ctx, `SELECT `+sourceColumns+` FROM velis.sources WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return sourceDomain.Source{}, sourceDomain.ErrNotFound
	}
	return src, err
}

func (r *SourceRepository) ClaimByID(ctx context.Context, id int64, owner string, now, leaseUntil time.Time) (sourceDomain.Source, error) {
	src, err := scanSource(r.pool.QueryRow(ctx, `UPDATE velis.sources SET lease_owner=$2, lease_expires_at=$4, updated_at=$3
WHERE id=$1 AND status IN ('active','degraded') AND (lease_expires_at IS NULL OR lease_expires_at <= $3)
RETURNING `+sourceColumns, id, owner, now, leaseUntil))
	if err == nil {
		return src, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sourceDomain.Source{}, err
	}
	existing, getErr := r.Get(ctx, id)
	if getErr != nil {
		return sourceDomain.Source{}, getErr
	}
	if existing.Status == sourceDomain.StatusPaused {
		return sourceDomain.Source{}, sourceDomain.ErrInvalidStatus
	}
	return sourceDomain.Source{}, sourceDomain.ErrLeaseHeld
}

func (r *SourceRepository) Pause(ctx context.Context, id int64, now time.Time) error {
	tag, err := r.pool.Exec(ctx, `UPDATE velis.sources SET status='paused', lease_owner=NULL, lease_expires_at=NULL, updated_at=$2 WHERE id=$1`, id, now)
	if err == nil && tag.RowsAffected() == 0 {
		return sourceDomain.ErrNotFound
	}
	return err
}

func (r *SourceRepository) Resume(ctx context.Context, id int64, now time.Time) error {
	tag, err := r.pool.Exec(ctx, `UPDATE velis.sources SET status='active', next_fetch_at=$2, lease_owner=NULL, lease_expires_at=NULL, updated_at=$2 WHERE id=$1`, id, now)
	if err == nil && tag.RowsAffected() == 0 {
		return sourceDomain.ErrNotFound
	}
	return err
}

func (r *SourceRepository) ClaimDue(ctx context.Context, owner string, now, leaseUntil time.Time, limit int) ([]sourceDomain.Source, error) {
	rows, err := r.pool.Query(ctx, `
WITH due AS (
  SELECT id FROM velis.sources
  WHERE status='active' AND next_fetch_at <= $1 AND (lease_expires_at IS NULL OR lease_expires_at <= $1)
  ORDER BY next_fetch_at, id
  FOR UPDATE SKIP LOCKED
  LIMIT $2
)
UPDATE velis.sources s
SET lease_owner=$3, lease_expires_at=$4, updated_at=$1
FROM due WHERE s.id=due.id
RETURNING `+returnedSourceColumns, now, limit, owner, leaseUntil)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]sourceDomain.Source, 0)
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, src)
	}
	return items, rows.Err()
}

func (r *SourceRepository) MarkNotModified(ctx context.Context, id int64, etag, lastModified *string, checkedAt, nextFetchAt time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE velis.sources SET etag=COALESCE($2, etag), last_modified=COALESCE($3, last_modified), last_checked_at=$4, last_success_at=$4, consecutive_failures=0, last_error_code=NULL, status='active', next_fetch_at=$5, lease_owner=NULL, lease_expires_at=NULL, updated_at=$4 WHERE id=$1`, id, etag, lastModified, checkedAt, nextFetchAt)
	return err
}

func (r *SourceRepository) MarkSuccess(ctx context.Context, id int64, metadata sourceDomain.Metadata, checkedAt, nextFetchAt time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE velis.sources SET title=$2, site_url=$3, etag=$4, last_modified=$5, last_checked_at=$6, last_success_at=$6, consecutive_failures=0, last_error_code=NULL, status='active', next_fetch_at=$7, lease_owner=NULL, lease_expires_at=NULL, updated_at=$6 WHERE id=$1`, id, metadata.Title, metadata.SiteURL, metadata.ETag, metadata.LastModified, checkedAt, nextFetchAt)
	return err
}

func (r *SourceRepository) MarkFailure(ctx context.Context, id int64, update sourceDomain.FailureUpdate) error {
	_, err := r.pool.Exec(ctx, `UPDATE velis.sources SET status=$2, consecutive_failures=$3, last_error_code=$4, last_checked_at=$5, next_fetch_at=$6, lease_owner=NULL, lease_expires_at=NULL, updated_at=$5 WHERE id=$1`, id, update.Status, update.ConsecutiveFailures, update.LastErrorCode, update.LastCheckedAt, update.NextFetchAt)
	return err
}

type scanner interface{ Scan(...any) error }

func scanSource(row scanner) (sourceDomain.Source, error) {
	var src sourceDomain.Source
	err := row.Scan(&src.ID, &src.FeedURL, &src.NormalizedFeedURL, &src.SiteURL, &src.Title, &src.Status,
		&src.ETag, &src.LastModified, &src.NextFetchAt, &src.LastCheckedAt, &src.LastSuccessAt,
		&src.ConsecutiveFailures, &src.LastErrorCode, &src.LeaseOwner, &src.LeaseExpiresAt,
		&src.CreatedAt, &src.UpdatedAt)
	return src, err
}
