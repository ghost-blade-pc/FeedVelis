package observability

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agenttools"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/prometheus/client_golang/prometheus"
)

func TestAgentObservationsOnlyFixedDimensions(t *testing.T) {
	registry := prometheus.NewRegistry()
	var logs bytes.Buffer
	o := NewAgentObserver(registry, slog.New(slog.NewJSONHandler(&logs, nil)))
	ctx := articlesearch.WithRequestID(context.Background(), "agent-request-123")
	o.ObserveConversation(ctx, "create", "success", time.Millisecond)
	o.ObserveConversation(ctx, "私有标题", "正文或游标", time.Millisecond)
	o.ObserveTool(ctx, "get_article", agenttools.ArticleNotFound, false, 0, false, time.Millisecond)
	o.ObserveTool(ctx, "查询或用户UUID", agenttools.Code("敏感底层失败"), true, 5, true, time.Millisecond)
	metrics, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var encoded strings.Builder
	for _, family := range metrics {
		encoded.WriteString(family.String())
	}
	if !strings.Contains(logs.String(), `"request_id":"agent-request-123"`) || strings.Contains(encoded.String(), "agent-request-123") {
		t.Fatal("request_id 必须仅保留于日志，不能成为指标标签")
	}
	all := encoded.String() + logs.String()
	for _, secret := range []string{"私有标题", "正文或游标", "查询或用户UUID", "敏感底层失败", "user_id", "conversation_id", "article_id", "cursor", "idempotency_key"} {
		if strings.Contains(all, secret) {
			t.Fatalf("观测泄露或高基数标签: %s", secret)
		}
	}
}
