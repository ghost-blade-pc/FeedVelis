package observability

import (
	"context"
	"time"

	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	ArticleSearchResults = []string{"success", "validation", "invalid_cursor", "search_unavailable", "dependency_unavailable", "internal"}
	ArticleSearchStages  = []string{"total", "opensearch", "postgres"}
	ArticleSearchPITOps  = []string{"create", "delete", "failure"}
)

type ArticleSearchMetrics struct {
	Requests   *prometheus.CounterVec
	Duration   *prometheus.HistogramVec
	Candidates prometheus.Counter
	Filtered   prometheus.Counter
	ScanLimit  prometheus.Counter
	PIT        *prometheus.CounterVec
}

func NewArticleSearchMetrics(registry prometheus.Registerer) *ArticleSearchMetrics {
	metrics := &ArticleSearchMetrics{
		Requests:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_article_search_requests_total", Help: "公开文章搜索的受控结果分类。"}, []string{"result"}),
		Duration:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "velis_article_search_duration_seconds", Help: "公开文章搜索的受控阶段耗时。"}, []string{"stage"}),
		Candidates: prometheus.NewCounter(prometheus.CounterOpts{Name: "velis_article_search_candidates_total", Help: "从 OpenSearch 检查的候选总数。"}),
		Filtered:   prometheus.NewCounter(prometheus.CounterOpts{Name: "velis_article_search_filtered_total", Help: "被 PostgreSQL 当前公开事实过滤的候选总数。"}),
		ScanLimit:  prometheus.NewCounter(prometheus.CounterOpts{Name: "velis_article_search_scan_limit_total", Help: "触及单请求候选扫描上限的次数。"}),
		PIT:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_article_search_pit_total", Help: "PIT 创建、删除和失败次数。"}, []string{"operation"}),
	}
	registry.MustRegister(metrics.Requests, metrics.Duration, metrics.Candidates, metrics.Filtered, metrics.ScanLimit, metrics.PIT)
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

type ArticleSearchObserver struct{ metrics *ArticleSearchMetrics }

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
