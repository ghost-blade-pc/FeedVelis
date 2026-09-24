package observability

import (
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
)

import enrichmentApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"

// AsyncMetrics 仅使用固定分类标签；事件、任务、Trace 与 URL 不得成为指标标签。
type AsyncMetrics struct {
	OutboxPending       prometheus.Gauge
	OutboxOldestSeconds prometheus.Gauge
	OutboxOperations    *prometheus.CounterVec
	ConsumerOperations  *prometheus.CounterVec
	TaskOperations      *prometheus.CounterVec
	DLQReplay           *prometheus.CounterVec
	MQReconnects        prometheus.Counter
	ComponentUp         *prometheus.GaugeVec
	AITasks             *prometheus.GaugeVec
	AIOldestSeconds     *prometheus.GaugeVec
	AIOperations        *prometheus.CounterVec
	AIModelDuration     *prometheus.HistogramVec
	AITokens            *prometheus.CounterVec
	AIErrors            *prometheus.CounterVec
}

func NewAsyncMetrics(registry prometheus.Registerer) *AsyncMetrics {
	m := &AsyncMetrics{
		OutboxPending:       prometheus.NewGauge(prometheus.GaugeOpts{Name: "velis_outbox_pending", Help: "尚未确认发布的 Outbox 事件数。"}),
		OutboxOldestSeconds: prometheus.NewGauge(prometheus.GaugeOpts{Name: "velis_outbox_oldest_age_seconds", Help: "最老待发布事件年龄。"}),
		OutboxOperations:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_outbox_operations_total", Help: "Outbox 操作计数。"}, []string{"operation", "result"}),
		ConsumerOperations:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_consumer_operations_total", Help: "Consumer 处理分类。"}, []string{"result"}),
		TaskOperations:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_async_task_operations_total", Help: "任务槽位变化。"}, []string{"operation"}),
		DLQReplay:           prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_dlq_replay_total", Help: "DLQ 人工重放结果。"}, []string{"result"}),
		MQReconnects:        prometheus.NewCounter(prometheus.CounterOpts{Name: "velis_mq_reconnects_total", Help: "MQ 重连次数。"}),
		ComponentUp:         prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "velis_worker_component_up", Help: "Worker 组件运行状态。"}, []string{"component"}),
		AITasks:             prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "velis_ai_tasks", Help: "按阶段和状态统计的 AI 任务数。"}, []string{"stage", "status"}),
		AIOldestSeconds:     prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "velis_ai_oldest_task_age_seconds", Help: "AI 任务最老年龄。"}, []string{"stage", "status"}),
		AIOperations:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_ai_operations_total", Help: "AI 任务操作结果。"}, []string{"stage", "operation", "result"}),
		AIModelDuration:     prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "velis_ai_model_duration_seconds", Help: "模型调用耗时。", Buckets: prometheus.DefBuckets}, []string{"stage", "result"}),
		AITokens:            prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_ai_tokens_total", Help: "Provider 返回的 Token usage。"}, []string{"stage", "type"}),
		AIErrors:            prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_ai_errors_total", Help: "AI 稳定错误分类。"}, []string{"stage", "code"}),
	}
	registry.MustRegister(m.OutboxPending, m.OutboxOldestSeconds, m.OutboxOperations, m.ConsumerOperations, m.TaskOperations, m.DLQReplay, m.MQReconnects, m.ComponentUp, m.AITasks, m.AIOldestSeconds, m.AIOperations, m.AIModelDuration, m.AITokens, m.AIErrors)
	for _, operation := range []string{"claim", "publish", "confirm", "failure", "cleanup"} {
		for _, result := range []string{"success", "failure"} {
			m.OutboxOperations.WithLabelValues(operation, result)
		}
	}
	for _, result := range []string{"applied", "noop", "duplicate", "requeue", "permanent_failure"} {
		m.ConsumerOperations.WithLabelValues(result)
	}
	for _, operation := range []string{"create", "update", "cancel"} {
		m.TaskOperations.WithLabelValues(operation)
	}
	for _, result := range []string{"confirmed", "failed"} {
		m.DLQReplay.WithLabelValues(result)
	}
	for _, component := range []string{"feed", "cleanup", "relay", "consumer", "outbox_cleanup", "metrics", "ai_enrichment"} {
		m.ComponentUp.WithLabelValues(component).Set(0)
	}
	for _, stage := range []string{"generation", "embedding"} {
		for _, status := range []string{"pending", "running", "retry_wait", "succeeded", "failed", "canceled"} {
			m.AITasks.WithLabelValues(stage, status).Set(0)
			m.AIOldestSeconds.WithLabelValues(stage, status).Set(0)
		}
	}
	return m
}

type AIObserver struct {
	metrics *AsyncMetrics
	logger  *slog.Logger
}

func NewAIObserver(metrics *AsyncMetrics, logger ...*slog.Logger) *AIObserver {
	observer := &AIObserver{metrics: metrics}
	if len(logger) > 0 {
		observer.logger = logger[0]
	}
	return observer
}
func (o *AIObserver) Claimed(task enrichmentApp.ClaimedTask) {
	o.metrics.AIOperations.WithLabelValues(task.Stage, "claim", "success").Inc()
	if o.logger != nil {
		o.logger.Info("AI 内容增强任务已认领", "task_id", task.ID, "task_generation", task.Generation,
			"stage", task.Stage, "article_id", task.ArticleID, "revision_id", task.RevisionID)
	}
}
func (o *AIObserver) Calls(task enrichmentApp.ClaimedTask, calls []enrichmentApp.CallRecord) {
	for _, call := range calls {
		if call.Kind == "" {
			continue
		}
		result := call.Status
		if result == "" {
			result = "failed"
		}
		o.metrics.AIModelDuration.WithLabelValues(task.Stage, result).Observe(call.Duration.Seconds())
		if call.Usage.InputTokens != nil {
			o.metrics.AITokens.WithLabelValues(task.Stage, "input").Add(float64(*call.Usage.InputTokens))
		}
		if call.Usage.OutputTokens != nil {
			o.metrics.AITokens.WithLabelValues(task.Stage, "output").Add(float64(*call.Usage.OutputTokens))
		}
		if call.Usage.TotalTokens != nil {
			o.metrics.AITokens.WithLabelValues(task.Stage, "total").Add(float64(*call.Usage.TotalTokens))
		}
		if call.ErrorCode != "" {
			o.metrics.AIErrors.WithLabelValues(task.Stage, string(call.ErrorCode)).Inc()
		}
	}
}
func (o *AIObserver) Finished(task enrichmentApp.ClaimedTask, result string, code enrichmentApp.ErrorCode) {
	o.metrics.AIOperations.WithLabelValues(task.Stage, "finish", result).Inc()
	if code != "" {
		o.metrics.AIErrors.WithLabelValues(task.Stage, string(code)).Inc()
	}
	if o.logger != nil {
		o.logger.Info("AI 内容增强任务执行结束", "task_id", task.ID, "task_generation", task.Generation,
			"stage", task.Stage, "article_id", task.ArticleID, "revision_id", task.RevisionID,
			"result", result, "error_code", code)
	}
}

var _ enrichmentApp.Observer = (*AIObserver)(nil)
