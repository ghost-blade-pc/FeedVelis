package rabbitmq

import (
	"context"
	"errors"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asynctask"
	"testing"
	"time"
)

type deliveryFake struct {
	body              []byte
	ack, reject, nack int
}

func (d *deliveryFake) Body() []byte      { return d.body }
func (d *deliveryFake) Ack() error        { d.ack++; return nil }
func (d *deliveryFake) Reject(bool) error { d.reject++; return nil }
func (d *deliveryFake) Nack(bool) error   { d.nack++; return nil }

type projectorFake struct{ err error }

func (p projectorFake) Project(context.Context, articleevent.Envelope) (asynctask.Outcome, error) {
	return asynctask.OutcomeApplied, p.err
}
func validBody() []byte {
	event, _ := articleevent.Deleted(context.Background(), 42, 81, 7, time.Now())
	data, _ := articleevent.Encode(event)
	return data
}
func TestConsumerAcknowledgementPolicy(t *testing.T) {
	dbErr := errors.New("connection refused")
	tests := []struct {
		name              string
		body              []byte
		projectErr        error
		db                bool
		ack, reject, nack int
		wantErr           error
	}{
		{"提交后 Ack", validBody(), nil, false, 1, 0, 0, nil}, {"永久契约错误", []byte(`{"bad":true}`), nil, false, 0, 1, 0, nil}, {"未分类错误", validBody(), errors.New("single message"), false, 0, 1, 0, nil}, {"数据库故障暂停", validBody(), dbErr, true, 0, 0, 0, ErrDatabaseUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			delivery := &deliveryFake{body: test.body}
			consumer := NewConsumer("", 16, projectorFake{err: test.projectErr}, func(err error) bool { return test.db && errors.Is(err, dbErr) })
			err := consumer.Handle(context.Background(), delivery)
			if !errors.Is(err, test.wantErr) || delivery.ack != test.ack || delivery.reject != test.reject || delivery.nack != test.nack {
				t.Fatalf("ack=%d reject=%d nack=%d err=%v", delivery.ack, delivery.reject, delivery.nack, err)
			}
		})
	}
}
func TestConsumerCancellationDoesNotAck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	delivery := &deliveryFake{body: validBody()}
	err := NewConsumer("", 16, projectorFake{err: ctx.Err()}, nil).Handle(ctx, delivery)
	if !errors.Is(err, context.Canceled) || delivery.ack+delivery.reject+delivery.nack != 0 {
		t.Fatalf("delivery=%+v err=%v", delivery, err)
	}
}
