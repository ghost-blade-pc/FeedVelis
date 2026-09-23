package observability

import "github.com/prometheus/client_golang/prometheus"

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
	}
	registry.MustRegister(m.OutboxPending, m.OutboxOldestSeconds, m.OutboxOperations, m.ConsumerOperations, m.TaskOperations, m.DLQReplay, m.MQReconnects, m.ComponentUp)
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
	for _, component := range []string{"feed", "cleanup", "relay", "consumer", "outbox_cleanup", "metrics"} {
		m.ComponentUp.WithLabelValues(component).Set(0)
	}
	return m
}
