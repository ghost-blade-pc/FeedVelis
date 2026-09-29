package observability

import (
	"context"
	"log/slog"
	"time"

	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	"github.com/prometheus/client_golang/prometheus"
)

type RecommendMetrics struct {
	Requests *prometheus.CounterVec
	Duration prometheus.Histogram
	Recall   *prometheus.CounterVec
	Filtered prometheus.Counter
	Latest   prometheus.Counter
	Sources  *prometheus.CounterVec
}

func NewRecommendMetrics(registry prometheus.Registerer) *RecommendMetrics {
	m := &RecommendMetrics{
		Requests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_recommend_requests_total", Help: "推荐模式、降级原因与结果。"}, []string{"mode", "reason", "result"}),
		Duration: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "velis_recommend_duration_seconds", Help: "推荐请求总耗时。"}),
		Recall:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_recommend_recall_total", Help: "固定召回通道的候选数。"}, []string{"path"}),
		Filtered: prometheus.NewCounter(prometheus.CounterOpts{Name: "velis_recommend_filtered_total", Help: "当前事实与反馈过滤数。"}),
		Latest:   prometheus.NewCounter(prometheus.CounterOpts{Name: "velis_recommend_latest_fill_total", Help: "latest 补位数。"}),
		Sources:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_recommend_source_total", Help: "推荐结果来源类型分布。"}, []string{"origin"}),
	}
	registry.MustRegister(m.Requests, m.Duration, m.Recall, m.Filtered, m.Latest, m.Sources)
	return m
}

type RecommendObserver struct {
	metrics *RecommendMetrics
	logger  *slog.Logger
}

var _ recommendation.Observer = (*RecommendObserver)(nil)

func NewRecommendObserver(metrics *RecommendMetrics, logger *slog.Logger) *RecommendObserver {
	return &RecommendObserver{metrics: metrics, logger: logger}
}

func (o *RecommendObserver) ObserveRequest(ctx context.Context, mode, reason, result string, duration time.Duration) {
	switch mode {
	case "personalized", "cold_start", "latest_fallback":
	default:
		mode = "unknown"
	}
	switch reason {
	case "", "search_unavailable", "semantic_unavailable", "candidate_shortage":
	default:
		reason = "unknown"
	}
	if result != "success" {
		result = "error"
	}
	o.metrics.Requests.WithLabelValues(mode, reason, result).Inc()
	o.metrics.Duration.Observe(duration.Seconds())
	if o.logger != nil {
		o.logger.InfoContext(ctx, "文章推荐请求", "request_id", searchApp.RequestID(ctx), "mode", mode, "reason", reason, "result", result, "duration_ms", duration.Milliseconds())
	}
}

func (o *RecommendObserver) AddRecall(_ context.Context, path string, count int) {
	if count < 0 {
		return
	}
	switch path {
	case "bm25", "knn", "union":
		o.metrics.Recall.WithLabelValues(path).Add(float64(count))
	}
}
func (o *RecommendObserver) AddFiltered(_ context.Context, count int) {
	if count >= 0 {
		o.metrics.Filtered.Add(float64(count))
	}
}
func (o *RecommendObserver) AddLatest(_ context.Context, count int) {
	if count >= 0 {
		o.metrics.Latest.Add(float64(count))
	}
}
func (o *RecommendObserver) AddSource(_ context.Context, origin string, count int) {
	if count < 0 || origin != "rss" && origin != "user" {
		return
	}
	o.metrics.Sources.WithLabelValues(origin).Add(float64(count))
}
