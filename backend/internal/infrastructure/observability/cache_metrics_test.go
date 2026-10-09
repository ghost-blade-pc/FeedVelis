package observability

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	cacheApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestCacheMetricsUnitsAndControlledLabels(t *testing.T) {
	var logs bytes.Buffer
	registry := prometheus.NewRegistry()
	m := NewCacheMetrics(registry, slog.New(slog.NewJSONHandler(&logs, nil)))
	ctx := context.Background()
	m.Count(ctx, cacheApp.Card, cacheApp.Get, cacheApp.Hit, cacheApp.None, 1)
	m.Count(ctx, cacheApp.Card, cacheApp.Get, cacheApp.Miss, cacheApp.None, 2)
	m.Count(ctx, cacheApp.Card, cacheApp.Get, cacheApp.Corrupt, cacheApp.Invalid, 1)
	if testutil.ToFloat64(m.Entries.WithLabelValues("card", "hit")) != 1 || testutil.ToFloat64(m.Entries.WithLabelValues("card", "miss")) != 2 || testutil.ToFloat64(m.Entries.WithLabelValues("card", "corrupt")) != 1 {
		t.Fatal("部分命中单位错误")
	}
	m.Count(ctx, cacheApp.Card, cacheApp.Get, cacheApp.Bypass, cacheApp.Timeout, 100)
	m.Count(ctx, cacheApp.Latest, cacheApp.Invalidate, cacheApp.Failure, cacheApp.Transport, 1)
	m.Count(ctx, cacheApp.Card, cacheApp.Set, cacheApp.Failure, cacheApp.Exhausted, 100)
	if testutil.ToFloat64(m.Failures.WithLabelValues("card", "get", "bypass", "timeout")) != 1 {
		t.Fatal("操作计数被条目数放大")
	}
	m.Fallback(ctx, cacheApp.Card, 1)
	m.ObserveDuration(ctx, cacheApp.Card, cacheApp.Get, cacheApp.None, 10*time.Millisecond)
	secret := "user-key-profile-cursor-content"
	m.Count(ctx, cacheApp.Object(secret), cacheApp.Get, cacheApp.Bypass, cacheApp.Transport, 1)
	m.Count(ctx, cacheApp.Card, cacheApp.Get, cacheApp.Bypass, cacheApp.Reason(secret), 1)
	m.ObserveDuration(ctx, cacheApp.Card, cacheApp.Operation(secret), cacheApp.None, time.Second)
	m.Fallback(ctx, cacheApp.Object(secret), 1)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if strings.Contains(label.GetValue(), secret) {
					t.Fatal("标签泄露")
				}
			}
		}
	}
	if strings.Contains(logs.String(), secret) || strings.Contains(logs.String(), "redis:") {
		t.Fatal("日志泄露")
	}
}
