package observability

import (
	"context"
	"log/slog"

	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// SearchMetrics 只使用受控分类标签：索引名、文章标题、正文与向量都不得成为标签。
type SearchMetrics struct {
	Jobs          *prometheus.GaugeVec
	OldestSeconds prometheus.Gauge
	Lagging       prometheus.Gauge
	Operations    *prometheus.CounterVec
	BulkItems     *prometheus.CounterVec
	Errors        *prometheus.CounterVec
	RebuildPhase  *prometheus.GaugeVec
	IndexInfo     *prometheus.GaugeVec
}

// SearchStatuses 与 SearchResultLabels 是固定标签集合，供初始化与测试核对有界性。
var (
	SearchStatuses      = []string{"pending", "running", "retry_wait", "succeeded", "failed"}
	SearchOperations    = []string{"claim", "deliver", "stale", "inconsistent", "finish"}
	SearchBulkResults   = []string{"created", "updated", "tombstoned", "noop", "retryable", "permanent", "unknown"}
	SearchRebuildPhases = []string{"snapshot", "catchup", "validate", "validated", "cutover", "serving", "completed", "abandoned", "failed"}
	SearchIndexRoles    = []string{"current", "rollback", "rebuild"}
)

func NewSearchMetrics(registry prometheus.Registerer) *SearchMetrics {
	m := &SearchMetrics{
		Jobs: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "velis_search_projection_jobs",
			Help: "按状态统计的投影槽位数量。"}, []string{"status"}),
		OldestSeconds: prometheus.NewGauge(prometheus.GaugeOpts{Name: "velis_search_oldest_pending_age_seconds",
			Help: "最老待处理投影槽位的年龄。"}),
		Lagging: prometheus.NewGauge(prometheus.GaugeOpts{Name: "velis_search_lagging_deliveries",
			Help: "尚未收敛的 delivery 数量。"}),
		Operations: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_search_projection_operations_total",
			Help: "投影执行结果。"}, []string{"operation", "result"}),
		BulkItems: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_search_bulk_items_total",
			Help: "Bulk item 逐项分类。"}, []string{"result"}),
		Errors: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_search_errors_total",
			Help: "投影稳定错误分类。"}, []string{"code"}),
		RebuildPhase: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "velis_search_rebuild_phase",
			Help: "索引重建阶段。"}, []string{"phase"}),
		IndexInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "velis_search_index_info",
			Help: "索引角色与 schema 版本；物理索引名不进入标签。"}, []string{"role", "schema_version"}),
	}
	registry.MustRegister(m.Jobs, m.OldestSeconds, m.Lagging, m.Operations, m.BulkItems, m.Errors, m.RebuildPhase, m.IndexInfo)
	for _, status := range SearchStatuses {
		m.Jobs.WithLabelValues(status).Set(0)
	}
	for _, operation := range SearchOperations {
		for _, result := range []string{"success", "failure", "stale"} {
			m.Operations.WithLabelValues(operation, result)
		}
	}
	for _, result := range SearchBulkResults {
		m.BulkItems.WithLabelValues(result)
	}
	for _, code := range projectionApp.ErrorCodes {
		m.Errors.WithLabelValues(string(code))
	}
	for _, phase := range SearchRebuildPhases {
		m.RebuildPhase.WithLabelValues(phase).Set(0)
	}
	for _, role := range SearchIndexRoles {
		m.IndexInfo.WithLabelValues(role, "unknown").Set(0)
	}
	return m
}

// SearchObserver 把投影执行与积压状态写入指标与结构化日志。
type SearchObserver struct {
	metrics *SearchMetrics
	logger  *slog.Logger
}

func NewSearchObserver(metrics *SearchMetrics, logger *slog.Logger) *SearchObserver {
	return &SearchObserver{metrics: metrics, logger: logger}
}

var _ projectionApp.Observer = (*SearchObserver)(nil)

func (o *SearchObserver) Claimed(job projectionApp.ClaimedJob, deliveries int) {
	o.metrics.Operations.WithLabelValues("claim", "success").Inc()
	if o.logger != nil {
		o.logger.Info("搜索投影任务已认领", "article_id", job.ArticleID,
			"projection_generation", job.Generation, "deliveries", deliveries)
	}
}

func (o *SearchObserver) Delivered(job projectionApp.ClaimedJob, outcome projectionApp.DeliveryOutcome) {
	normalized := outcome.Normalize()
	o.metrics.BulkItems.WithLabelValues(string(normalized.Result)).Inc()
	o.metrics.Operations.WithLabelValues("deliver", "success").Inc()
	if normalized.Code != projectionApp.ErrorUnclassified {
		o.metrics.Errors.WithLabelValues(string(normalized.Code)).Inc()
	}
	if o.logger != nil {
		o.logger.Info("搜索投影投递完成", "article_id", job.ArticleID, "projection_generation", job.Generation,
			"result", normalized.Result, "error_code", normalized.Code)
	}
}

func (o *SearchObserver) Staled(job projectionApp.ClaimedJob, generation int64) {
	o.metrics.Operations.WithLabelValues("stale", "stale").Inc()
	if o.logger != nil {
		o.logger.Info("搜索投影执行已失效", "article_id", job.ArticleID, "projection_generation", generation)
	}
}

func (o *SearchObserver) Inconsistent(job projectionApp.ClaimedJob) {
	o.metrics.Operations.WithLabelValues("inconsistent", "success").Inc()
	o.metrics.Errors.WithLabelValues(string(projectionApp.ErrorUnclassified)).Inc()
	if o.logger != nil {
		o.logger.Warn("搜索投影发现 AI 当前选择不一致，已忽略该向量", "article_id", job.ArticleID,
			"projection_generation", job.Generation)
	}
}

func (o *SearchObserver) Finished(job projectionApp.ClaimedJob, outcome projectionApp.DeliveryOutcome) {
	o.metrics.Operations.WithLabelValues("finish", "success").Inc()
}

func (o *SearchObserver) Observe(backlog projectionApp.Backlog) {
	o.metrics.OldestSeconds.Set(backlog.OldestSeconds)
	o.metrics.Lagging.Set(float64(backlog.LaggingDeliveries))
	o.metrics.Jobs.WithLabelValues("pending").Set(float64(backlog.Pending))
	o.metrics.Jobs.WithLabelValues("failed").Set(float64(backlog.Failing))
}

func (o *SearchObserver) Rebuilt(state projectionApp.RebuildState, action string) {
	for _, phase := range SearchRebuildPhases {
		value := 0.0
		if string(state.Phase) == phase {
			value = 1
		}
		o.metrics.RebuildPhase.WithLabelValues(phase).Set(value)
	}
	o.metrics.IndexInfo.WithLabelValues("rebuild", "unknown").Set(0)
	if o.logger != nil {
		o.logger.Info("索引重建状态变化", "rebuild_id", state.ID, "phase", state.Phase, "action", action,
			"candidate_index", state.CandidateIndex)
	}
}

// RefreshSearchBacklog 统计待处理与失败槽位、落后 delivery 与最老年龄。
// 它只聚合计数，不读取任何正文。
func RefreshSearchBacklog(ctx context.Context, pool *pgxpool.Pool, metrics *SearchMetrics) error {
	var pending, failing, oldestSeconds float64
	err := pool.QueryRow(ctx, `SELECT
count(*) FILTER (WHERE status IN ('pending','retry_wait','running')),
count(*) FILTER (WHERE status='failed'),
COALESCE(EXTRACT(EPOCH FROM clock_timestamp() - min(target_changed_at) FILTER (WHERE status IN ('pending','retry_wait','running'))), 0)
FROM velis.search_projection_jobs`).Scan(&pending, &failing, &oldestSeconds)
	if err != nil {
		return err
	}
	var lagging float64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM velis.search_projection_deliveries d
JOIN velis.search_projection_jobs j ON j.article_id=d.article_id
WHERE d.required_generation<>j.generation OR d.status<>'succeeded'`).Scan(&lagging); err != nil {
		return err
	}
	metrics.Jobs.WithLabelValues("pending").Set(pending)
	metrics.Jobs.WithLabelValues("failed").Set(failing)
	metrics.OldestSeconds.Set(oldestSeconds)
	metrics.Lagging.Set(lagging)
	return nil
}
