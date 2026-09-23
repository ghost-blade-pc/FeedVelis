package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

type OutboxRepository struct{ pool *pgxpool.Pool }

func NewOutboxRepository(pool *pgxpool.Pool) *OutboxRepository { return &OutboxRepository{pool: pool} }

func (r *OutboxRepository) Append(ctx context.Context, event articleevent.Envelope) error {
	tx, ok := transactionFromContext(ctx)
	if !ok {
		return ports.ErrTransactionRequired
	}
	data, err := articleevent.Encode(event)
	if err != nil {
		return err
	}
	eventID, _ := uuid.Parse(event.EventID)
	_, err = tx.Exec(ctx, `INSERT INTO velis.outbox_events
(event_id,event_type,aggregate_type,aggregate_id,aggregate_version,envelope,occurred_at,created_at,next_attempt_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,clock_timestamp(),clock_timestamp())`, eventID, event.EventType, event.Aggregate.Type,
		event.Aggregate.ID, event.Aggregate.Version, data, event.OccurredAt)
	return err
}

func (r *OutboxRepository) Get(ctx context.Context, eventID string) (articleevent.Envelope, error) {
	var data []byte
	err := querier(ctx, r.pool).QueryRow(ctx, `SELECT envelope FROM velis.outbox_events WHERE event_id=$1`, eventID).Scan(&data)
	if err != nil {
		return articleevent.Envelope{}, err
	}
	return articleevent.Decode(data)
}

func (r *OutboxRepository) ExistsAggregateEvent(ctx context.Context, aggregateID string, version int64, eventType string) (bool, error) {
	var exists bool
	err := querier(ctx, r.pool).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM velis.outbox_events
WHERE aggregate_type='article' AND aggregate_id=$1 AND aggregate_version=$2 AND event_type=$3)`, aggregateID, version, eventType).Scan(&exists)
	return exists, err
}

func scanEnvelope(row pgx.Row) (articleevent.Envelope, error) {
	var raw json.RawMessage
	if err := row.Scan(&raw); err != nil {
		return articleevent.Envelope{}, err
	}
	if len(raw) == 0 {
		return articleevent.Envelope{}, errors.New("Outbox envelope 为空")
	}
	return articleevent.Decode(raw)
}
