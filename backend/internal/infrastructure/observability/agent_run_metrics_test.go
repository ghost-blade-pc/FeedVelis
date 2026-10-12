package observability

import (
	"bytes"
	"context"
	"encoding/json"
	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	"github.com/prometheus/client_golang/prometheus"
	"log/slog"
	"strings"
	"testing"
)

func TestAgentRunMetricsSafeAndUnknownUsage(t *testing.T) {
	reg := prometheus.NewRegistry()
	var logs bytes.Buffer
	o := NewAgentRunObserver(reg, slog.New(slog.NewJSONHandler(&logs, nil)))
	o.ObserveRun(context.Background(), agent.Observation{State: run.State("私有用户"), ErrorCode: run.Code("Bearer secret"), Usage: run.Usage{ModelCalls: 1, ToolCalls: 2}})
	o.ObserveRun(context.Background(), agent.Observation{State: run.Canceled, ErrorCode: run.CodeAuthInvalid})
	o.ObserveRun(context.Background(), agent.Observation{State: run.Failed, ErrorCode: run.CodeCitationInvalid})
	metrics, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	all := logs.String()
	for _, m := range metrics {
		all += m.String()
	}
	for _, value := range []string{"私有用户", "Bearer secret", "user_id", "article_id", "run_id", "prompt", "api_key"} {
		if strings.Contains(all, value) {
			t.Fatal(value, all)
		}
	}
	if !strings.Contains(all, "AGENT_CITATION_INVALID") || !strings.Contains(all, "AUTH_SESSION_INVALID") {
		t.Fatal(all)
	}
	raw, _ := json.Marshal(run.Usage{})
	if !strings.Contains(string(raw), `"input_tokens":null`) || strings.Contains(string(raw), `"output_tokens":0`) {
		t.Fatal(string(raw))
	}
}
