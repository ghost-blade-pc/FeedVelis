package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// APIKey 不接受 YAML，不继承内容增强或 Embedding 凭据，打印时使用 SecretBytes 脱敏。
type AgentGenerationConfig struct {
	Enabled            bool          `yaml:"enabled"`
	Provider           string        `yaml:"provider"`
	BaseURL            string        `yaml:"base_url"`
	Model              string        `yaml:"model"`
	APIKey             SecretBytes   `yaml:"-"`
	ProfileVersion     string        `yaml:"profile_version"`
	PromptVersion      string        `yaml:"prompt_version"`
	TimeoutRaw         string        `yaml:"timeout"`
	Timeout            time.Duration `yaml:"-"`
	MaxTokensParameter string        `yaml:"max_tokens_parameter"`
	MaxOutputTokens    int           `yaml:"max_output_tokens"`
}
type AgentExecutionConfig struct {
	TimeoutRaw       string        `yaml:"timeout"`
	Timeout          time.Duration `yaml:"-"`
	MaxModelCalls    int           `yaml:"max_model_calls"`
	MaxToolCalls     int           `yaml:"max_tool_calls"`
	MaxOutputTokens  int           `yaml:"max_output_tokens"`
	AuditTokenBudget int           `yaml:"audit_token_budget"`
	MaxInputBytes    int           `yaml:"max_input_bytes"`
	MaxAnswerChars   int           `yaml:"max_answer_chars"`
	Concurrency      int           `yaml:"concurrency"`
}

func (a AgentConfig) GenerationAvailable(authEnabled bool) bool {
	g := a.Generation
	return authEnabled && a.Enabled && g.Enabled && g.Provider == "openai-compatible" && strings.TrimSpace(g.BaseURL) != "" && strings.TrimSpace(g.Model) != "" && g.APIKey.IsSet()
}
func applyAgentGenerationEnvironment(a *AgentConfig) error {
	g, e := &a.Generation, &a.Execution
	if err := setBool(&g.Enabled, "VELIS_AGENT_GENERATION_ENABLED"); err != nil {
		return err
	}
	for target, key := range map[*string]string{&g.Provider: "VELIS_AGENT_GENERATION_PROVIDER", &g.BaseURL: "VELIS_AGENT_GENERATION_BASE_URL", &g.Model: "VELIS_AGENT_GENERATION_MODEL", &g.ProfileVersion: "VELIS_AGENT_GENERATION_PROFILE_VERSION", &g.PromptVersion: "VELIS_AGENT_GENERATION_PROMPT_VERSION", &g.TimeoutRaw: "VELIS_AGENT_GENERATION_TIMEOUT", &g.MaxTokensParameter: "VELIS_AGENT_GENERATION_MAX_TOKENS_PARAMETER", &e.TimeoutRaw: "VELIS_AGENT_RUN_TIMEOUT"} {
		setString(target, key)
	}
	if value, ok := os.LookupEnv("VELIS_AGENT_GENERATION_API_KEY"); ok {
		g.APIKey = SecretBytes([]byte(strings.TrimSpace(value)))
	}
	for target, key := range map[*int]string{&g.MaxOutputTokens: "VELIS_AGENT_GENERATION_MAX_OUTPUT_TOKENS", &e.MaxModelCalls: "VELIS_AGENT_MAX_MODEL_CALLS", &e.MaxToolCalls: "VELIS_AGENT_MAX_TOOL_CALLS", &e.MaxOutputTokens: "VELIS_AGENT_RUN_MAX_OUTPUT_TOKENS", &e.AuditTokenBudget: "VELIS_AGENT_RUN_AUDIT_TOKEN_BUDGET", &e.MaxInputBytes: "VELIS_AGENT_MAX_INPUT_BYTES", &e.MaxAnswerChars: "VELIS_AGENT_MAX_ANSWER_CHARS", &e.Concurrency: "VELIS_AGENT_EXECUTION_CONCURRENCY"} {
		if err := setInt(target, key); err != nil {
			return err
		}
	}
	return nil
}
func validateAgentGeneration(a *AgentConfig) error {
	g, e := &a.Generation, &a.Execution
	var err error
	if g.Timeout, err = durationWithin("agent.generation.timeout", g.TimeoutRaw, 100*time.Millisecond, 60*time.Second); err != nil {
		return err
	}
	if e.Timeout, err = durationWithin("agent.execution.timeout", e.TimeoutRaw, 5*time.Second, 120*time.Second); err != nil {
		return err
	}
	for _, bound := range []struct {
		name            string
		value, min, max int
	}{
		{"generation.max_output_tokens", g.MaxOutputTokens, 256, 8192}, {"execution.max_model_calls", e.MaxModelCalls, 2, 8}, {"execution.max_tool_calls", e.MaxToolCalls, 1, 12},
		{"execution.max_output_tokens", e.MaxOutputTokens, 512, 32768}, {"execution.audit_token_budget", e.AuditTokenBudget, 8192, 262144}, {"execution.max_input_bytes", e.MaxInputBytes, 8192, 131072},
		{"execution.max_answer_chars", e.MaxAnswerChars, 1000, 16000}, {"execution.concurrency", e.Concurrency, 1, 16},
	} {
		if bound.value < bound.min || bound.value > bound.max {
			return fmt.Errorf("agent.%s 必须介于 %d 与 %d", bound.name, bound.min, bound.max)
		}
	}
	if e.MaxOutputTokens < g.MaxOutputTokens || e.AuditTokenBudget <= g.MaxOutputTokens {
		return errors.New("agent 累计输出不得小于单次输出，审计预算必须大于单次输出")
	}
	if strings.TrimSpace(g.ProfileVersion) == "" || strings.TrimSpace(g.PromptVersion) == "" {
		return errors.New("agent 模型与提示词版本不能为空")
	}
	if g.MaxTokensParameter != "max_tokens" && g.MaxTokensParameter != "max_completion_tokens" {
		return errors.New("agent.generation.max_tokens_parameter 必须为 max_tokens 或 max_completion_tokens")
	}
	if g.Provider != "" && g.Provider != "openai-compatible" {
		return errors.New("agent.generation.provider 仅支持 openai-compatible")
	}
	if g.BaseURL != "" {
		parsed, err := url.Parse(g.BaseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("agent.generation.base_url 必须为无凭据、查询及片段的完整 HTTP(S) URL")
		}
	}
	// 缺少模型配置仅使新接受不可用；会话、重放及维护仍可启动。
	return nil
}
