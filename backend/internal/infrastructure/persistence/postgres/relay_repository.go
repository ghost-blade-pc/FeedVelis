package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/outbox"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/relay"
)

type RelayRepository struct{ pool *pgxpool.Pool }

func NewRelayRepository(pool *pgxpool.Pool) *RelayRepository { return &RelayRepository{pool: pool} }

func (r *RelayRepository) Claim(ctx context.Context, owner string, limit int, lease time.Duration) ([]relay.ClaimedEvent, error) {
	if owner == "" || limit < 1 || limit > 1000 || lease <= 0 {
		return nil, fmt.Errorf("Relay 认领参数无效")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `WITH candidates AS (
SELECT event_id FROM velis.outbox_events WHERE published_at IS NULL AND next_attempt_at<=clock_timestamp()
AND (lease_expires_at IS NULL OR lease_expires_at<=clock_timestamp()) ORDER BY next_attempt_at,occurred_at,event_id
FOR UPDATE SKIP LOCKED LIMIT $1)
UPDATE velis.outbox_events o SET lease_owner=$2,lease_token=gen_random_uuid(),lease_expires_at=clock_timestamp()+$3::interval,
last_attempt_at=clock_timestamp(),publish_attempts=publish_attempts+1 FROM candidates c WHERE o.event_id=c.event_id
RETURNING o.envelope,o.lease_token::text,o.publish_attempts`, limit, owner, lease.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	claimed := make([]relay.ClaimedEvent, 0, limit)
	for rows.Next() {
		var raw []byte
		var item relay.ClaimedEvent
		if err := rows.Scan(&raw, &item.LeaseToken, &item.Attempts); err != nil {
			return nil, err
		}
		item.Event, err = articleevent.Decode(raw)
		if err != nil {
			return nil, err
		}
		claimed = append(claimed, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return claimed, nil
}

func (r *RelayRepository) DeletePublishedBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit < 1 || limit > 10000 {
		return 0, fmt.Errorf("清理批量无效")
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM velis.outbox_events WHERE event_id IN (
SELECT event_id FROM velis.outbox_events WHERE published_at IS NOT NULL AND published_at<$1 ORDER BY published_at,event_id LIMIT $2)`, before, limit)
	return tag.RowsAffected(), err
}

func (r *RelayRepository) PendingStats(ctx context.Context) (outbox.Stats, error) {
	var stats outbox.Stats
	var seconds *float64
	err := r.pool.QueryRow(ctx, `SELECT count(*),EXTRACT(EPOCH FROM clock_timestamp()-min(created_at)) FROM velis.outbox_events WHERE published_at IS NULL`).Scan(&stats.Pending, &seconds)
	if seconds != nil {
		stats.OldestAge = time.Duration(*seconds * float64(time.Second))
	}
	return stats, err
}

func (r *RelayRepository) Confirm(ctx context.Context, eventID, token string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE velis.outbox_events SET published_at=clock_timestamp(),lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,last_error_code=NULL,last_error_message=NULL WHERE event_id=$1 AND lease_token=$2 AND published_at IS NULL`, eventID, token)
	return tag.RowsAffected() == 1, err
}

func (r *RelayRepository) Fail(ctx context.Context, eventID, token, code, message string, delay time.Duration) (bool, error) {
	if len(code) > 64 {
		code = code[:64]
	}
	if len(message) > 512 {
		message = message[:512]
	}
	tag, err := r.pool.Exec(ctx, `UPDATE velis.outbox_events SET lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,last_error_code=$3,last_error_message=$4,next_attempt_at=clock_timestamp()+$5::interval WHERE event_id=$1 AND lease_token=$2 AND published_at IS NULL`, eventID, token, code, message, delay.String())
	return tag.RowsAffected() == 1, err
}
