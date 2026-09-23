package observability

import (
	"context"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asynctask"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/outbox"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/relay"
)

type RelayStore struct {
	delegate relay.OutboxStore
	metrics  *AsyncMetrics
}

func InstrumentRelayStore(delegate relay.OutboxStore, metrics *AsyncMetrics) *RelayStore {
	return &RelayStore{delegate: delegate, metrics: metrics}
}

func (s *RelayStore) Claim(ctx context.Context, owner string, batch int, lease time.Duration) ([]relay.ClaimedEvent, error) {
	events, err := s.delegate.Claim(ctx, owner, batch, lease)
	s.metrics.OutboxOperations.WithLabelValues("claim", metricResult(err)).Inc()
	s.refreshStats(ctx)
	return events, err
}

func (s *RelayStore) Confirm(ctx context.Context, eventID, token string) (bool, error) {
	ok, err := s.delegate.Confirm(ctx, eventID, token)
	result := "success"
	if err != nil || !ok {
		result = "failure"
	}
	s.metrics.OutboxOperations.WithLabelValues("confirm", result).Inc()
	s.refreshStats(ctx)
	return ok, err
}

func (s *RelayStore) Fail(ctx context.Context, eventID, token, code, message string, delay time.Duration) (bool, error) {
	ok, err := s.delegate.Fail(ctx, eventID, token, code, message, delay)
	result := "success"
	if err != nil || !ok {
		result = "failure"
	}
	s.metrics.OutboxOperations.WithLabelValues("failure", result).Inc()
	s.refreshStats(ctx)
	return ok, err
}

func (s *RelayStore) refreshStats(ctx context.Context) {
	maintenance, ok := s.delegate.(interface {
		PendingStats(context.Context) (outbox.Stats, error)
	})
	if !ok {
		return
	}
	stats, err := maintenance.PendingStats(ctx)
	if err == nil {
		s.metrics.OutboxPending.Set(float64(stats.Pending))
		s.metrics.OutboxOldestSeconds.Set(stats.OldestAge.Seconds())
	}
}

type Publisher struct {
	delegate relay.Publisher
	metrics  *AsyncMetrics
}

func InstrumentPublisher(delegate relay.Publisher, metrics *AsyncMetrics) *Publisher {
	return &Publisher{delegate: delegate, metrics: metrics}
}

func (p *Publisher) Publish(ctx context.Context, event articleevent.Envelope) (relay.PublishResult, error) {
	result, err := p.delegate.Publish(ctx, event)
	p.metrics.OutboxOperations.WithLabelValues("publish", metricResult(err)).Inc()
	return result, err
}

type Projector struct {
	delegate interface {
		Project(context.Context, articleevent.Envelope) (asynctask.Outcome, error)
	}
	metrics *AsyncMetrics
}

func InstrumentProjector(delegate interface {
	Project(context.Context, articleevent.Envelope) (asynctask.Outcome, error)
}, metrics *AsyncMetrics) *Projector {
	return &Projector{delegate: delegate, metrics: metrics}
}

func (p *Projector) Project(ctx context.Context, event articleevent.Envelope) (asynctask.Outcome, error) {
	outcome, err := p.delegate.Project(ctx, event)
	result := string(outcome)
	if err != nil {
		result = "requeue"
	}
	if result == "" {
		result = "noop"
	}
	p.metrics.ConsumerOperations.WithLabelValues(result).Inc()
	if outcome == asynctask.OutcomeApplied {
		operation := "update"
		switch event.EventType {
		case articleevent.PublishedType:
			operation = "create"
		case articleevent.OfflinedType, articleevent.DeletedType:
			operation = "cancel"
		}
		p.metrics.TaskOperations.WithLabelValues(operation).Inc()
	}
	return outcome, err
}

type OutboxMaintenance struct {
	delegate outbox.Maintenance
	metrics  *AsyncMetrics
}

func InstrumentOutboxMaintenance(delegate outbox.Maintenance, metrics *AsyncMetrics) *OutboxMaintenance {
	return &OutboxMaintenance{delegate: delegate, metrics: metrics}
}

func (m *OutboxMaintenance) DeletePublishedBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	deleted, err := m.delegate.DeletePublishedBefore(ctx, before, limit)
	m.metrics.OutboxOperations.WithLabelValues("cleanup", metricResult(err)).Inc()
	stats, statsErr := m.delegate.PendingStats(ctx)
	if statsErr == nil {
		m.metrics.OutboxPending.Set(float64(stats.Pending))
		m.metrics.OutboxOldestSeconds.Set(stats.OldestAge.Seconds())
	}
	return deleted, err
}

func (m *OutboxMaintenance) PendingStats(ctx context.Context) (outbox.Stats, error) {
	return m.delegate.PendingStats(ctx)
}

func metricResult(err error) string {
	if err != nil {
		return "failure"
	}
	return "success"
}
