package observability

import (
	"context"
	"log/slog"
	"time"

	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	ArticleSearchResults = []string{"success", "validation", "invalid_cursor", "search_unavailable", "dependency_unavailable", "internal"}
	ArticleSearchStages  = []string{"total", "opensearch", "postgres", "embedding", "knn", "rrf"}
	ArticleSearchPITOps  = []string{"create", "delete", "failure"}
)

type ArticleSearchMetrics struct {
	Requests          *prometheus.CounterVec
	Duration          *prometheus.HistogramVec
	Candidates        prometheus.Counter
	Filtered          prometheus.Counter
	ScanLimit         prometheus.Counter
	PIT               *prometheus.CounterVec
	Modes             *prometheus.CounterVec
	Recall            *prometheus.CounterVec
	Stale             prometheus.Counter
	DependencyFailure *prometheus.CounterVec
}

func NewArticleSearchMetrics(registry prometheus.Registerer) *ArticleSearchMetrics {
	metrics := &ArticleSearchMetrics{
		DependencyFailure: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_article_search_dependency_failures_total", Help: "查询依赖的受控失败分类。"}, []string{"dependency", "class"}),
		Modes:             prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_article_search_modes_total", Help: "实际模式及受控降级原因。"}, []string{"mode", "reason"}),
		Recall:            prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_article_search_recall_total", Help: "各路召回及去重候选量。"}, []string{"path"}),
		Stale:             prometheus.NewCounter(prometheus.CounterOpts{Name: "velis_article_search_stale_identity_total", Help: "陈旧向量身份过滤量。"}),
		Requests:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_article_search_requests_total", Help: "公开文章搜索的受控结果分类。"}, []string{"result"}),
		Duration:          prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "velis_article_search_duration_seconds", Help: "公开文章搜索的受控阶段耗时。"}, []string{"stage"}),
		Candidates:        prometheus.NewCounter(prometheus.CounterOpts{Name: "velis_article_search_candidates_total", Help: "从 OpenSearch 检查的候选总数。"}),
		Filtered:          prometheus.NewCounter(prometheus.CounterOpts{Name: "velis_article_search_filtered_total", Help: "被 PostgreSQL 当前公开事实过滤的候选总数。"}),
		ScanLimit:         prometheus.NewCounter(prometheus.CounterOpts{Name: "velis_article_search_scan_limit_total", Help: "触及单请求候选扫描上限的次数。"}),
		PIT:               prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_article_search_pit_total", Help: "PIT 创建、删除和失败次数。"}, []string{"operation"}),
	}
	registry.MustRegister(metrics.Requests, metrics.Duration, metrics.Candidates, metrics.Filtered, metrics.ScanLimit, metrics.PIT, metrics.Modes, metrics.Recall, metrics.Stale, metrics.DependencyFailure)
	for _, result := range ArticleSearchResults {
		metrics.Requests.WithLabelValues(result)
	}
	for _, stage := range ArticleSearchStages {
		metrics.Duration.WithLabelValues(stage)
	}
	for _, operation := range ArticleSearchPITOps {
		metrics.PIT.WithLabelValues(operation)
	}
	return metrics
}

type ArticleSearchObserver struct {
	metrics *ArticleSearchMetrics
	logger  *slog.Logger
}

func NewArticleSearchObserver(metrics *ArticleSearchMetrics) *ArticleSearchObserver {
	return &ArticleSearchObserver{metrics: metrics}
}

var _ searchApp.Observer = (*ArticleSearchObserver)(nil)

func (o *ArticleSearchObserver) ObserveRequest(_ context.Context, result searchApp.Result, duration time.Duration) {
	o.metrics.Requests.WithLabelValues(string(result)).Inc()
	o.metrics.Duration.WithLabelValues(string(searchApp.StageTotal)).Observe(duration.Seconds())
}
func (o *ArticleSearchObserver) ObserveStage(_ context.Context, stage searchApp.Stage, duration time.Duration) {
	o.metrics.Duration.WithLabelValues(string(stage)).Observe(duration.Seconds())
}
func (o *ArticleSearchObserver) AddCandidates(_ context.Context, count int) {
	o.metrics.Candidates.Add(float64(count))
}
func (o *ArticleSearchObserver) AddFiltered(_ context.Context, count int) {
	o.metrics.Filtered.Add(float64(count))
}
func (o *ArticleSearchObserver) ObserveScanLimit(context.Context) { o.metrics.ScanLimit.Inc() }
func (o *ArticleSearchObserver) ObservePIT(_ context.Context, operation searchApp.PITOperation) {
	o.metrics.PIT.WithLabelValues(string(operation)).Inc()
}

func (o *ArticleSearchObserver) ObserveMode(ctx context.Context, mode searchApp.RetrievalMode, reason string) {
	name := "bm25"
	if mode == searchApp.ModeHybrid {
		name = "hybrid"
	}
	switch reason {
	case "", "disabled", "embedding_unconfigured", "embedding_timeout", "embedding_failed", "invalid_vector", "schema_incompatible", "knn_timeout", "knn_failed", "no_valid_vector":
	default:
		reason = "embedding_failed"
	}
	o.metrics.Modes.WithLabelValues(name, reason).Inc()
	if o.logger != nil {
		o.logger.InfoContext(ctx, "文章搜索检索模式", "request_id", searchApp.RequestID(ctx), "mode", name, "reason", reason)
	}
}
func (o *ArticleSearchObserver) AddRecall(_ context.Context, path string, n int) {
	switch path {
	case "bm25", "knn", "union":
	default:
		return
	}
	if n >= 0 {
		o.metrics.Recall.WithLabelValues(path).Add(float64(n))
	}
}
func (o *ArticleSearchObserver) AddStale(_ context.Context, n int) {
	if n >= 0 {
		o.metrics.Stale.Add(float64(n))
	}
}

func (o *ArticleSearchObserver) ObserveDependencyFailure(ctx context.Context, dependency, class string) {
	switch dependency {
	case "embedding", "knn", "opensearch", "postgres":
	default:
		return
	}
	switch class {
	case "timeout", "canceled", "rate_limited", "authentication", "provider_unavailable", "network", "input_unsupported", "invalid_output", "internal":
	default:
		class = "internal"
	}
	o.metrics.DependencyFailure.WithLabelValues(dependency, class).Inc()
	if o.logger != nil {
		o.logger.WarnContext(ctx, "文章搜索依赖失败", "request_id", searchApp.RequestID(ctx), "dependency", dependency, "class", class)
	}
}

func (o *ArticleSearchObserver) WithLogger(logger *slog.Logger) *ArticleSearchObserver {
	o.logger = logger
	return o
}
