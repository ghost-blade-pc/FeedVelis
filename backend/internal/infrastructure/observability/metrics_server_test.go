package observability

import (
	"context"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asynctask"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/outbox"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/relay"
	"github.com/prometheus/client_golang/prometheus"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type metricsRelayStore struct{}

func (metricsRelayStore) Claim(context.Context, string, int, time.Duration) ([]relay.ClaimedEvent, error) {
	return nil, nil
}
func (metricsRelayStore) Confirm(context.Context, string, string) (bool, error) { return true, nil }
func (metricsRelayStore) Fail(context.Context, string, string, string, string, time.Duration) (bool, error) {
	return true, nil
}

type metricsPublisher struct{}

func (metricsPublisher) Publish(context.Context, articleevent.Envelope) (relay.PublishResult, error) {
	return relay.PublishConfirmed, nil
}

type metricsProjector struct{}

func (metricsProjector) Project(context.Context, articleevent.Envelope) (asynctask.Outcome, error) {
	return asynctask.OutcomeApplied, nil
}

type metricsMaintenance struct{}

func (metricsMaintenance) DeletePublishedBefore(context.Context, time.Time, int) (int64, error) {
	return 2, nil
}
func (metricsMaintenance) PendingStats(context.Context) (outbox.Stats, error) {
	return outbox.Stats{Pending: 3, OldestAge: 4 * time.Second}, nil
}

func TestMetricsEndpointUsesOnlyLowCardinalityLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewAsyncMetrics(registry)
	metrics.OutboxPending.Set(2)
	request := httptest.NewRequest("GET", "/metrics", nil)
	response := httptest.NewRecorder()
	NewMetricsServer("127.0.0.1:0", registry).Handler().ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != 200 || !strings.Contains(body, "velis_outbox_pending 2") {
		t.Fatalf("code=%d body=%s", response.Code, body)
	}
	for _, metric := range []string{"velis_outbox_operations_total", "velis_consumer_operations_total", "velis_async_task_operations_total", "velis_dlq_replay_total", "velis_mq_reconnects_total", "velis_worker_component_up"} {
		if !strings.Contains(body, metric) {
			t.Errorf("缺少指标 %s", metric)
		}
	}
	for _, forbidden := range []string{"event_id", "task_id", "trace_id", "rabbitmq_url", "password", "markdown"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("指标泄露高基数字段 %q", forbidden)
		}
	}
}

func TestRuntimeInstrumentationUpdatesLowCardinalityMetrics(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewAsyncMetrics(registry)
	ctx := context.Background()
	store := InstrumentRelayStore(metricsRelayStore{}, metrics)
	_, _ = store.Claim(ctx, "worker-secret-id", 10, time.Minute)
	_, _ = store.Confirm(ctx, "event-secret-id", "lease-secret-id")
	_, _ = store.Fail(ctx, "event-secret-id", "lease-secret-id", "code", "body-secret", time.Second)
	_, _ = InstrumentPublisher(metricsPublisher{}, metrics).Publish(ctx, articleevent.Envelope{})
	_, _ = InstrumentProjector(metricsProjector{}, metrics).Project(ctx, articleevent.Envelope{})
	_, _ = InstrumentOutboxMaintenance(metricsMaintenance{}, metrics).DeletePublishedBefore(ctx, time.Now(), 10)

	request := httptest.NewRequest("GET", "/metrics", nil)
	response := httptest.NewRecorder()
	NewMetricsServer("127.0.0.1:0", registry).Handler().ServeHTTP(response, request)
	body := response.Body.String()
	for _, expected := range []string{
		`velis_outbox_pending 3`,
		`velis_outbox_oldest_age_seconds 4`,
		`velis_outbox_operations_total{operation="claim",result="success"} 1`,
		`velis_outbox_operations_total{operation="publish",result="success"} 1`,
		`velis_consumer_operations_total{result="applied"} 1`,
		`velis_async_task_operations_total{operation="update"} 1`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("缺少运行指标 %q", expected)
		}
	}
	for _, secret := range []string{"worker-secret-id", "event-secret-id", "lease-secret-id", "body-secret"} {
		if strings.Contains(body, secret) {
			t.Errorf("指标泄露 %q", secret)
		}
	}
}
