package observability

import (
	"context"
	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	"github.com/prometheus/client_golang/prometheus"
	"log/slog"
	"strconv"
)

type AgentRunObserver struct {
	finished        *prometheus.CounterVec
	calls           *prometheus.CounterVec
	duration, first prometheus.Histogram
	tokens          *prometheus.CounterVec
	logger          *slog.Logger
}

func NewAgentRunObserver(reg prometheus.Registerer, logger *slog.Logger) *AgentRunObserver {
	o := &AgentRunObserver{
		finished: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_agent_runs_total", Help: "回合终态、安全错误与用量完整性。"}, []string{"status", "error_code", "usage_complete"}),
		calls:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_agent_run_calls_total", Help: "回合内已预留的模型和工具尝试。"}, []string{"kind"}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "velis_agent_run_duration_seconds", Help: "回合执行耗时。", Buckets: []float64{.1, .5, 1, 2, 5, 10, 30, 60, 120}}),
		first:    prometheus.NewHistogram(prometheus.HistogramOpts{Name: "velis_agent_run_first_block_seconds", Help: "接受至首个已提交片段的耗时。", Buckets: []float64{.1, .5, 1, 2, 5, 10, 30, 60, 120}}),
		tokens:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_agent_run_reported_tokens_total", Help: "仅完整报告的 Provider 用量，不估算缺失值。"}, []string{"kind"}), logger: logger,
	}
	reg.MustRegister(o.finished, o.calls, o.duration, o.first, o.tokens)
	return o
}
func (o *AgentRunObserver) ObserveRun(ctx context.Context, v agent.Observation) {
	state := "unknown"
	switch v.State {
	case run.Succeeded, run.Failed, run.Canceled, run.TimedOut:
		state = string(v.State)
	}
	code := v.ErrorCode
	if code != "" && !code.Valid() {
		code = run.CodeInternal
	}
	o.finished.WithLabelValues(state, string(code), strconv.FormatBool(v.Usage.Complete)).Inc()
	if v.Usage.ModelCalls >= 0 && v.Usage.ModelCalls <= 8 {
		o.calls.WithLabelValues("model").Add(float64(v.Usage.ModelCalls))
	}
	if v.Usage.ToolCalls >= 0 && v.Usage.ToolCalls <= 12 {
		o.calls.WithLabelValues("tool").Add(float64(v.Usage.ToolCalls))
	}
	if v.Duration >= 0 {
		o.duration.Observe(v.Duration.Seconds())
	}
	if v.FirstBlock > 0 {
		o.first.Observe(v.FirstBlock.Seconds())
	}
	if v.Usage.Complete {
		for kind, value := range map[string]*int64{"input": v.Usage.InputTokens, "output": v.Usage.OutputTokens, "total": v.Usage.TotalTokens} {
			if value != nil && *value >= 0 {
				o.tokens.WithLabelValues(kind).Add(float64(*value))
			}
		}
	}
	if o.logger != nil {
		o.logger.InfoContext(ctx, "Agent 回合结束", "status", state, "error_code", code, "usage_complete", v.Usage.Complete, "model_calls", v.Usage.ModelCalls, "tool_calls", v.Usage.ToolCalls, "duration_ms", v.Duration.Milliseconds())
	}
}
