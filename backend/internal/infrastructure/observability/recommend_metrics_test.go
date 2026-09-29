package observability

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func recommendCount(t *testing.T, metric prometheus.Metric) float64 {
	t.Helper()
	value := &dto.Metric{}
	if err := metric.Write(value); err != nil {
		t.Fatal(err)
	}
	return value.GetCounter().GetValue()
}

func TestRecommendMetricsUseFixedLabelsAndControlledLog(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewRecommendMetrics(registry)
	var log bytes.Buffer
	observer := NewRecommendObserver(metrics, slog.New(slog.NewTextHandler(&log, nil)))
	ctx := searchApp.WithRequestID(context.Background(), "request-1")
	observer.AddRecall(ctx, "bm25", 3)
	observer.AddRecall(ctx, "knn", 2)
	observer.AddRecall(ctx, "union", 4)
	observer.AddRecall(ctx, "cursor-secret", 999)
	observer.AddFiltered(ctx, 1)
	observer.AddLatest(ctx, 2)
	observer.AddSource(ctx, "rss", 2)
	observer.AddSource(ctx, "user", 1)
	observer.AddSource(ctx, "article-123", 9)
	observer.ObserveRequest(ctx, "latest_fallback", "search_unavailable", "success", 15*time.Millisecond)
	observer.ObserveRequest(ctx, "profile-secret", "vector-secret", "cursor-secret", time.Millisecond)
	if recommendCount(t, metrics.Recall.WithLabelValues("bm25")) != 3 || recommendCount(t, metrics.Filtered) != 1 || recommendCount(t, metrics.Latest) != 2 || recommendCount(t, metrics.Sources.WithLabelValues("rss")) != 2 {
		t.Fatal("召回/过滤/补位/来源指标错误")
	}
	if recommendCount(t, metrics.Requests.WithLabelValues("latest_fallback", "search_unavailable", "success")) != 1 || recommendCount(t, metrics.Requests.WithLabelValues("unknown", "unknown", "error")) != 1 {
		t.Fatal("模式/降级指标错误")
	}
	for _, secret := range []string{"profile-secret", "vector-secret", "cursor-secret", "article-123"} {
		if strings.Contains(log.String(), secret) {
			t.Fatalf("受控日志泄露 %q: %s", secret, log.String())
		}
	}
	if !strings.Contains(log.String(), "request-1") {
		t.Fatal("日志缺少 request ID")
	}
}
