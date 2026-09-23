package postgres

import (
	"context"
	"fmt"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asynctask"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

type ConsumedEventRepository struct{ consumerName string }

func NewConsumedEventRepository(consumerName string) (*ConsumedEventRepository, error) {
	if consumerName == "" || len(consumerName) > 96 {
		return nil, asynctask.ErrInvalidConsumerName
	}
	return &ConsumedEventRepository{consumerName: consumerName}, nil
}

func (r *ConsumedEventRepository) Start(ctx context.Context, event articleevent.Envelope) (bool, error) {
	tx, ok := transactionFromContext(ctx)
	if !ok {
		return false, ports.ErrTransactionRequired
	}
	tag, err := tx.Exec(ctx, `INSERT INTO velis.consumed_events
(consumer_name,event_id,event_type,aggregate_type,aggregate_id,aggregate_version,result,processed_at)
VALUES ($1,$2,$3,$4,$5,$6,'noop',clock_timestamp()) ON CONFLICT DO NOTHING`,
		r.consumerName, event.EventID, event.EventType, event.Aggregate.Type, event.Aggregate.ID, event.Aggregate.Version)
	if err != nil {
		return false, fmt.Errorf("登记消费事件: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *ConsumedEventRepository) Finish(ctx context.Context, eventID string, result asynctask.ConsumeResult) error {
	if result != asynctask.ResultApplied && result != asynctask.ResultNoop {
		return fmt.Errorf("消费结果无效: %s", result)
	}
	tx, ok := transactionFromContext(ctx)
	if !ok {
		return ports.ErrTransactionRequired
	}
	tag, err := tx.Exec(ctx, `UPDATE velis.consumed_events SET result=$3,processed_at=clock_timestamp()
WHERE consumer_name=$1 AND event_id=$2`, r.consumerName, eventID, result)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("消费事件记录不存在")
	}
	return nil
}
