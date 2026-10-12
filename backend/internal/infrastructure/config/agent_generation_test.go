package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentGenerationDefaultsAndIndependentCredentials(t *testing.T) {
	c := Default()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	g, e := c.Agent.Generation, c.Agent.Execution
	if g.Enabled || g.APIKey.IsSet() || g.ProfileVersion != "agent-model-v1" || g.PromptVersion != "agent-reading-v1" || g.Timeout != 30*time.Second || g.MaxTokensParameter != "max_tokens" || g.MaxOutputTokens != 2048 || e.Timeout != time.Minute || e.MaxModelCalls != 4 || e.MaxToolCalls != 6 || e.MaxOutputTokens != 8192 || e.AuditTokenBudget != 65536 || e.MaxInputBytes != 49152 || e.MaxAnswerChars != 8000 || e.Concurrency != 4 {
		t.Fatal(g, e)
	}
	path := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(path, []byte("agent:\n  enabled: true\n  generation:\n    enabled: true\n    provider: openai-compatible\n    base_url: http://localhost:1234/v1\n    model: local\n    api_key: yaml-secret-ignored\n    max_output_tokens: 1024\n  execution:\n    concurrency: 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VELIS_AI_GENERATION_API_KEY", "enhancement-secret")
	t.Setenv("VELIS_AGENT_GENERATION_API_KEY", "")
	// 只调用 Agent 环境加载，避免启用另一模块的不完整示例配置。
	if err := applyAgentEnvironment(&c); err != nil {
		t.Fatal(err)
	}
	if c.Agent.Generation.APIKey.IsSet() {
		t.Fatal("继承内容增强密钥")
	}
	c, err := Load(path)
	if err == nil {
		t.Fatal("内容增强的独立部分配置应被其原校验拒绝")
	}
	t.Setenv("VELIS_AI_GENERATION_API_KEY", "")
	c, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Agent.Generation.APIKey.IsSet() || c.Agent.GenerationAvailable(true) {
		t.Fatal("接受 YAML 密钥或缺模型可用性检查")
	}
	t.Setenv("VELIS_AGENT_GENERATION_API_KEY", "agent-secret-only")
	t.Setenv("VELIS_AGENT_GENERATION_MAX_OUTPUT_TOKENS", "1536")
	t.Setenv("VELIS_AGENT_EXECUTION_CONCURRENCY", "3")
	t.Setenv("VELIS_AGENT_RUN_TIMEOUT", "20s")
	c, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Agent.GenerationAvailable(true) || c.Agent.GenerationAvailable(false) || c.Agent.Generation.MaxOutputTokens != 1536 || c.Agent.Execution.Concurrency != 3 || c.Agent.Execution.Timeout != 20*time.Second {
		t.Fatal(c.Agent)
	}
	for _, s := range []string{fmt.Sprint(c.Agent), fmt.Sprintf("%+v", c.Agent), fmt.Sprintf("%#v", c.Agent)} {
		if strings.Contains(s, "agent-secret-only") {
			t.Fatal("格式化泄露密钥")
		}
	}
}

func TestAgentGenerationRangesAndCrossFields(t *testing.T) {
	for _, mutate := range []func(*AgentConfig){
		func(a *AgentConfig) { a.Generation.TimeoutRaw = "99ms" }, func(a *AgentConfig) { a.Generation.TimeoutRaw = "61s" }, func(a *AgentConfig) { a.Execution.TimeoutRaw = "4s" }, func(a *AgentConfig) { a.Execution.TimeoutRaw = "121s" },
		func(a *AgentConfig) { a.Generation.MaxOutputTokens = 255 }, func(a *AgentConfig) { a.Generation.MaxOutputTokens = 8193 }, func(a *AgentConfig) { a.Execution.MaxModelCalls = 1 }, func(a *AgentConfig) { a.Execution.MaxModelCalls = 9 }, func(a *AgentConfig) { a.Execution.MaxToolCalls = 0 }, func(a *AgentConfig) { a.Execution.MaxToolCalls = 13 },
		func(a *AgentConfig) { a.Execution.MaxOutputTokens = 511 }, func(a *AgentConfig) { a.Execution.MaxOutputTokens = 32769 }, func(a *AgentConfig) { a.Execution.AuditTokenBudget = 8191 }, func(a *AgentConfig) { a.Execution.AuditTokenBudget = 262145 },
		func(a *AgentConfig) { a.Execution.MaxInputBytes = 8191 }, func(a *AgentConfig) { a.Execution.MaxInputBytes = 131073 }, func(a *AgentConfig) { a.Execution.MaxAnswerChars = 999 }, func(a *AgentConfig) { a.Execution.MaxAnswerChars = 16001 }, func(a *AgentConfig) { a.Execution.Concurrency = 0 }, func(a *AgentConfig) { a.Execution.Concurrency = 17 },
		func(a *AgentConfig) { a.Generation.MaxOutputTokens = 8192; a.Execution.MaxOutputTokens = 8191 }, func(a *AgentConfig) { a.Generation.MaxOutputTokens = 8192; a.Execution.AuditTokenBudget = 8192 },
		func(a *AgentConfig) { a.Generation.ProfileVersion = " " }, func(a *AgentConfig) { a.Generation.PromptVersion = "" }, func(a *AgentConfig) { a.Generation.MaxTokensParameter = "both" }, func(a *AgentConfig) { a.Generation.Provider = "other" },
		func(a *AgentConfig) { a.Generation.BaseURL = "https://secret@host/v1" }, func(a *AgentConfig) { a.Generation.BaseURL = "https://host/v1?key=secret" }, func(a *AgentConfig) { a.Generation.BaseURL = "file:///tmp/a" },
	} {
		a := defaultAgentConfig()
		mutate(&a)
		if validateAgentConfig(&a) == nil {
			t.Fatal("非法配置未拒绝", a)
		}
	}
	for _, maximal := range []bool{false, true} {
		a := defaultAgentConfig()
		g, e := &a.Generation, &a.Execution
		if maximal {
			g.TimeoutRaw = "60s"
			g.MaxOutputTokens = 8192
			e.TimeoutRaw = "120s"
			e.MaxModelCalls = 8
			e.MaxToolCalls = 12
			e.MaxOutputTokens = 32768
			e.AuditTokenBudget = 262144
			e.MaxInputBytes = 131072
			e.MaxAnswerChars = 16000
			e.Concurrency = 16
		} else {
			g.TimeoutRaw = "100ms"
			g.MaxOutputTokens = 256
			e.TimeoutRaw = "5s"
			e.MaxModelCalls = 2
			e.MaxToolCalls = 1
			e.MaxOutputTokens = 512
			e.AuditTokenBudget = 8192
			e.MaxInputBytes = 8192
			e.MaxAnswerChars = 1000
			e.Concurrency = 1
		}
		if err := validateAgentConfig(&a); err != nil {
			t.Fatal("合法边界", err)
		}
	}
}

func TestAgentGenerationExamplesAndEnvironmentPassThrough(t *testing.T) {
	c, err := Load("../../../configs/config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Agent.Generation.Enabled || c.Agent.Execution.MaxModelCalls != 4 || c.Agent.Execution.MaxToolCalls != 6 || c.Agent.Generation.APIKey.IsSet() {
		t.Fatal("示例默认值", c.Agent)
	}
	compose, err := os.ReadFile("../../../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	env, err := os.ReadFile("../../../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile("../../../../docs/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"VELIS_AGENT_GENERATION_ENABLED", "VELIS_AGENT_GENERATION_PROVIDER", "VELIS_AGENT_GENERATION_BASE_URL", "VELIS_AGENT_GENERATION_MODEL", "VELIS_AGENT_GENERATION_API_KEY", "VELIS_AGENT_GENERATION_PROFILE_VERSION", "VELIS_AGENT_GENERATION_PROMPT_VERSION", "VELIS_AGENT_GENERATION_TIMEOUT", "VELIS_AGENT_GENERATION_MAX_TOKENS_PARAMETER", "VELIS_AGENT_GENERATION_MAX_OUTPUT_TOKENS", "VELIS_AGENT_RUN_TIMEOUT", "VELIS_AGENT_MAX_MODEL_CALLS", "VELIS_AGENT_MAX_TOOL_CALLS", "VELIS_AGENT_RUN_MAX_OUTPUT_TOKENS", "VELIS_AGENT_RUN_AUDIT_TOKEN_BUDGET", "VELIS_AGENT_MAX_INPUT_BYTES", "VELIS_AGENT_MAX_ANSWER_CHARS", "VELIS_AGENT_EXECUTION_CONCURRENCY"} {
		if !strings.Contains(string(compose), key+": ${"+key) || !strings.Contains(string(env), key+"=") || !strings.Contains(string(doc), key) {
			t.Fatal("配置示例/透传缺失", key)
		}
	}
}
