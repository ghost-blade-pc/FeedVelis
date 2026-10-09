package observability

import (
	"context"
	"log/slog"
	"time"

	cacheApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/prometheus/client_golang/prometheus"
)

type CacheMetrics struct {
	Entries         *prometheus.CounterVec
	Failures        *prometheus.CounterVec
	FallbackBatches *prometheus.CounterVec
	Duration        *prometheus.HistogramVec
	logger          *slog.Logger
}

var _ cacheApp.Observer = (*CacheMetrics)(nil)

func NewCacheMetrics(reg prometheus.Registerer, logger *slog.Logger) *CacheMetrics {
	m := &CacheMetrics{
		Entries:         prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_cache_entries_total", Help: "缓存读取条目计数；损坏同时计入未命中。"}, []string{"object", "result"}),
		Failures:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_cache_failures_total", Help: "缓存故障绕过、回填和失效失败操作数。"}, []string{"object", "operation", "result", "reason"}),
		FallbackBatches: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_cache_fallback_batches_total", Help: "应用实际执行的数据库批量回源或推荐计划重建次数。"}, []string{"object"}),
		Duration:        prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "velis_cache_operation_duration_seconds", Help: "缓存操作耗时，包含连接、排队及响应；预算绕过也记录。", Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.2}}, []string{"object", "operation", "reason"}), logger: logger,
	}
	reg.MustRegister(m.Entries, m.Failures, m.FallbackBatches, m.Duration)
	return m
}
func validCacheLabels(object cacheApp.Object, op cacheApp.Operation, reason cacheApp.Reason) bool {
	if object != cacheApp.Card && object != cacheApp.Latest && object != cacheApp.Recommend {
		return false
	}
	if op != cacheApp.Get && op != cacheApp.Set && op != cacheApp.Invalidate {
		return false
	}
	switch reason {
	case cacheApp.None, cacheApp.Invalid, cacheApp.Transport, cacheApp.Timeout, cacheApp.Exhausted, cacheApp.Canceled:
		return true
	}
	return false
}
func (m *CacheMetrics) Count(ctx context.Context, object cacheApp.Object, op cacheApp.Operation, result cacheApp.Result, reason cacheApp.Reason, n int) {
	if n <= 0 || !validCacheLabels(object, op, reason) {
		return
	}
	switch result {
	case cacheApp.Hit, cacheApp.Miss, cacheApp.Corrupt:
		if op == cacheApp.Get {
			m.Entries.WithLabelValues(string(object), string(result)).Add(float64(n))
		}
	case cacheApp.Bypass, cacheApp.Failure:
		m.Failures.WithLabelValues(string(object), string(op), string(result), string(reason)).Inc()
		if m.logger != nil {
			m.logger.WarnContext(ctx, "读取缓存操作绕过", "request_id", searchApp.RequestID(ctx), "object", string(object), "operation", string(op), "result", string(result), "reason", string(reason))
		}
	}
}
func (m *CacheMetrics) ObserveDuration(_ context.Context, object cacheApp.Object, op cacheApp.Operation, reason cacheApp.Reason, d time.Duration) {
	if d < 0 || !validCacheLabels(object, op, reason) {
		return
	}
	m.Duration.WithLabelValues(string(object), string(op), string(reason)).Observe(d.Seconds())
}

// Fallback 由编排在数据库回源或推荐计划重建时调用，n 为批次数，不能用 miss 数代替。
func (m *CacheMetrics) Fallback(_ context.Context, object cacheApp.Object, n int) {
	if n > 0 && validCacheLabels(object, cacheApp.Get, cacheApp.None) {
		m.FallbackBatches.WithLabelValues(string(object)).Add(float64(n))
	}
}
