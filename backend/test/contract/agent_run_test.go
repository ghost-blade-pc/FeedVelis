package contract

import (
	"encoding/json"
	"fmt"
	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/handler"
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentRunOpenAPIAndClientFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi/velis.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var api map[string]any
	if err := yaml.Unmarshal(raw, &api); err != nil {
		t.Fatal(err)
	}
	schemas := api["components"].(map[string]any)["schemas"].(map[string]any)
	paths := api["paths"].(map[string]any)
	root := "/me/agent/conversations/{id}/runs"
	for _, suffix := range []string{"", "/{run_id}", "/{run_id}/cancel", "/{run_id}/events"} {
		if paths[root+suffix] == nil {
			t.Fatal("未登记路由", suffix)
		}
	}
	raw, err = os.ReadFile("../fixtures/agent-runs/protocol.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Command     agent.Command          `json:"command"`
		Accepted    agent.AcceptedSnapshot `json:"accepted"`
		Events      []agent.Event          `json:"events"`
		Positions   []string               `json:"positions"`
		Answer      agentevidence.Answer   `json:"answer"`
		StreamError map[string]any         `json:"stream_error"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.NormalizeCommand(fixture.Command); err != nil {
		t.Fatal(err)
	}
	check := func(name string, value any) {
		t.Helper()
		encoded, _ := json.Marshal(value)
		var v any
		_ = json.Unmarshal(encoded, &v)
		if err := validateSchema(schemas, schemas[name].(map[string]any), v, name); err != nil {
			t.Fatal(err)
		}
	}
	check("AgentRunInput", fixture.Command)
	check("AgentRunAccepted", fixture.Accepted)
	check("AgentAnswer", fixture.Answer)
	check("AgentRunDetail", agent.Detail{Summary: agent.Summary{AcceptedSnapshot: fixture.Accepted}, Answer: &fixture.Answer, Usage: run.Usage{}})
	for i, event := range fixture.Events {
		check("AgentRunEvent", event)
		if fixture.Positions[i] != handler.EventID(event.RunID, event.Sequence) {
			t.Fatal("客户端位置与JSON不一致")
		}
	}
	if len(fixture.StreamError) != 2 || fixture.StreamError["id"] != nil || fixture.StreamError["sequence"] != nil {
		t.Fatal("瞬态错误携带位置")
	}
	for _, name := range []string{"clarification", "redacted"} {
		raw, err := os.ReadFile("../../internal/application/agentorchestration/testdata/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var a agentevidence.Answer
		_ = json.Unmarshal(raw, &a)
		check("AgentAnswer", a)
	}
	doc, err := os.ReadFile("../../../docs/agent-runs.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"Last-Event-ID", "replaceSnapshot", "stream.error", "EOF", "cancel", "fetch", "Bearer", "protocol.json"} {
		if !strings.Contains(string(doc), word) {
			t.Fatal("客户端文档缺少契约", word)
		}
	}
}

// 检查本契约使用的结构、必需字段和额外字段，输入是实际 Go 序列化结果。
func validateSchema(all map[string]any, s map[string]any, value any, path string) error {
	if ref, ok := s["$ref"].(string); ok {
		return validateSchema(all, all[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any), value, path)
	}
	if choices, ok := s["anyOf"].([]any); ok {
		for _, choice := range choices {
			if validateSchema(all, choice.(map[string]any), value, path) == nil {
				return nil
			}
		}
		return fmt.Errorf("%s 不满足 anyOf", path)
	}
	if c, ok := s["const"]; ok && fmt.Sprint(c) != fmt.Sprint(value) {
		return fmt.Errorf("%s const", path)
	}
	if choices, ok := s["enum"].([]any); ok {
		found := false
		for _, choice := range choices {
			if fmt.Sprint(choice) == fmt.Sprint(value) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s enum: %v", path, value)
		}
	}
	switch s["type"] {
	case "null":
		if value != nil {
			return fmt.Errorf("%s 应为null", path)
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s 应为string", path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s 应为bool", path)
		}
	case "integer":
		if n, ok := value.(float64); !ok || n != float64(int64(n)) {
			return fmt.Errorf("%s 应为integer", path)
		}
	case "array":
		a, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s 应为array", path)
		}
		for i, v := range a {
			if err := validateSchema(all, s["items"].(map[string]any), v, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case "object":
		o, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s 应为object", path)
		}
		props, _ := s["properties"].(map[string]any)
		required, _ := s["required"].([]any)
		for _, key := range required {
			if _, ok := o[key.(string)]; !ok {
				return fmt.Errorf("%s 缺少 %s", path, key)
			}
		}
		for key, v := range o {
			p, ok := props[key]
			if !ok && s["additionalProperties"] == false {
				return fmt.Errorf("%s 额外字段 %s", path, key)
			}
			if ok {
				if err := validateSchema(all, p.(map[string]any), v, path+"."+key); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func TestAgentRunIntegrationEntryRequiresEnvironmentAndRejectsSkip(t *testing.T) {
	names := []string{"VELIS_TEST_DATABASE_URL", "VELIS_TEST_REDIS_ADDRESS", "VELIS_TEST_OPENSEARCH_URL", "VELIS_TEST_NGINX_IMAGE"}
	for _, missing := range names {
		t.Run(missing, func(t *testing.T) {
			cmd := exec.Command("bash", "../../../scripts/integration-agent-runs.sh")
			env := []string{}
			for _, entry := range os.Environ() {
				keep := true
				for _, name := range names {
					if strings.HasPrefix(entry, name+"=") {
						keep = false
					}
				}
				if keep {
					env = append(env, entry)
				}
			}
			for _, name := range names {
				if name != missing {
					env = append(env, name+"=local-fixture")
				}
			}
			cmd.Env = env
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), missing) {
				t.Fatal("缺依赖未拒绝", string(output), err)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "skip.json")
	_ = os.WriteFile(path, []byte("{\"Action\":\"pass\",\"Test\":\"ok\"}\n{\"Action\":\"skip\",\"Test\":\"missing\"}\n"), 0600)
	output, err := exec.Command("python3", "../../../scripts/check-go-test-json.py", path).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "skip") {
		t.Fatal(string(output), err)
	}
}
