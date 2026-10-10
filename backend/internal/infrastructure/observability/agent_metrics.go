package observability

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agenttools"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/prometheus/client_golang/prometheus"
)

type AgentObserver struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	items    *prometheus.HistogramVec
	logger   *slog.Logger
}

func NewAgentObserver(registry prometheus.Registerer, logger *slog.Logger) *AgentObserver {
	o := &AgentObserver{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "velis_agent_requests_total", Help: "会话及内部工具的固定操作与结果。"}, []string{"operation", "result", "degraded", "truncated"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "velis_agent_duration_seconds", Help: "会话及内部工具耗时。"}, []string{"operation"}),
		items:    prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "velis_agent_tool_items", Help: "工具结果数量。", Buckets: []float64{0, 1, 5, 10}}, []string{"operation"}), logger: logger,
	}
	registry.MustRegister(o.requests, o.duration, o.items)
	return o
}

func agentResult(result string) string {
	switch result {
	case "success", "VALIDATION_FAILED", "INVALID_CURSOR", "IDEMPOTENCY_KEY_REQUIRED", "IDEMPOTENCY_KEY_INVALID", "IF_MATCH_REQUIRED", "IF_MATCH_INVALID", "AGENT_CONVERSATION_NOT_FOUND", "IDEMPOTENCY_KEY_REUSED", "IDEMPOTENCY_IN_PROGRESS", "AGENT_TITLE_VERSION_CONFLICT", "AGENT_CONVERSATION_LIMIT_EXCEEDED", "AGENT_MESSAGE_LIMIT_EXCEEDED", "DEPENDENCY_UNAVAILABLE", "INTERNAL_ERROR", "ARTICLE_NOT_FOUND", "SEARCH_UNAVAILABLE", "TOOL_TIMEOUT", "TOOL_CANCELED":
		return result
	default:
		return "INTERNAL_ERROR"
	}
}

func (o *AgentObserver) record(ctx context.Context, operation, result string, degraded, truncated bool, duration time.Duration) {
	result = agentResult(result)
	o.requests.WithLabelValues(operation, result, strconv.FormatBool(degraded), strconv.FormatBool(truncated)).Inc()
	o.duration.WithLabelValues(operation).Observe(duration.Seconds())
	if o.logger != nil {
		o.logger.InfoContext(ctx, "Agent 操作", "request_id", articlesearch.RequestID(ctx), "operation", operation, "result", result, "degraded", degraded, "truncated", truncated, "duration_ms", duration.Milliseconds())
	}
}

func (o *AgentObserver) ObserveConversation(ctx context.Context, operation, result string, duration time.Duration) {
	switch operation {
	case "create", "list", "detail", "rename", "delete", "history", "append":
	default:
		operation = "unknown"
	}
	o.record(ctx, "conversation."+operation, result, false, false, duration)
}

func (o *AgentObserver) ObserveTool(ctx context.Context, tool string, code agenttools.Code, degraded bool, count int, truncated bool, duration time.Duration) {
	switch tool {
	case "search_articles", "recommend_articles", "get_article":
	default:
		tool = "unknown"
	}
	result := string(code)
	if result == "" {
		result = "success"
	}
	o.record(ctx, tool, result, degraded, truncated, duration)
	if count >= 0 && count <= 10 {
		o.items.WithLabelValues(tool).Observe(float64(count))
	}
}
