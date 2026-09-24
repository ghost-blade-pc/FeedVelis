package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// AIConfig 描述可选的内容增强模型与执行边界。
type AIConfig struct {
	Generation GenerationConfig `yaml:"generation"`
	Embedding  EmbeddingConfig  `yaml:"embedding"`
	Worker     AIWorkerConfig   `yaml:"worker"`
}

// ModelProfileConfig 是一个独立 OpenAI-compatible 模型配置。
type ModelProfileConfig struct {
	Provider       string        `yaml:"provider"`
	BaseURL        string        `yaml:"base_url"`
	APIKey         string        `yaml:"api_key"`
	Model          string        `yaml:"model"`
	ProfileVersion string        `yaml:"profile_version"`
	TimeoutRaw     string        `yaml:"timeout"`
	BudgetRaw      string        `yaml:"budget"`
	Timeout        time.Duration `yaml:"-"`
	Budget         time.Duration `yaml:"-"`
}

func (p ModelProfileConfig) Enabled() bool {
	return strings.TrimSpace(p.Provider) != "" || strings.TrimSpace(p.BaseURL) != "" ||
		strings.TrimSpace(p.APIKey) != "" || strings.TrimSpace(p.Model) != ""
}

type GenerationConfig struct {
	Profile          ModelProfileConfig `yaml:",inline"`
	WorkflowVersion  string             `yaml:"workflow_version"`
	PromptVersion    string             `yaml:"prompt_version"`
	SingleInputChars int                `yaml:"single_input_chars"`
	ChunkChars       int                `yaml:"chunk_chars"`
	MaxChunks        int                `yaml:"max_chunks"`
	ChunkConcurrency int                `yaml:"chunk_concurrency"`
	MaxCalls         int                `yaml:"max_calls"`
	MaxAttempts      int                `yaml:"max_attempts"`
	BackoffMinRaw    string             `yaml:"backoff_min"`
	BackoffMaxRaw    string             `yaml:"backoff_max"`
	BackoffMin       time.Duration      `yaml:"-"`
	BackoffMax       time.Duration      `yaml:"-"`
	MaxOutputTokens  int                `yaml:"max_output_tokens"`
	AuditTokenBudget int                `yaml:"audit_token_budget"`
	SummaryMaxChars  int                `yaml:"summary_max_chars"`
	KeywordMaxCount  int                `yaml:"keyword_max_count"`
	TopicMaxCount    int                `yaml:"topic_max_count"`
	LabelMaxChars    int                `yaml:"label_max_chars"`
}

type EmbeddingConfig struct {
	Profile          ModelProfileConfig `yaml:",inline"`
	InputVersion     string             `yaml:"input_version"`
	Dimensions       int                `yaml:"dimensions"`
	MaxInputChars    int                `yaml:"max_input_chars"`
	MaxAttempts      int                `yaml:"max_attempts"`
	BackoffMinRaw    string             `yaml:"backoff_min"`
	BackoffMaxRaw    string             `yaml:"backoff_max"`
	BackoffMin       time.Duration      `yaml:"-"`
	BackoffMax       time.Duration      `yaml:"-"`
	AuditTokenBudget int                `yaml:"audit_token_budget"`
}

type AIWorkerConfig struct {
	LeaseRaw  string        `yaml:"lease"`
	PollRaw   string        `yaml:"poll_interval"`
	BatchSize int           `yaml:"batch_size"`
	Lease     time.Duration `yaml:"-"`
	Poll      time.Duration `yaml:"-"`
}

func applyAIEnvironment(cfg *Config) error {
	gen := &cfg.AI.Generation
	embed := &cfg.AI.Embedding
	for target, key := range map[*string]string{
		&gen.Profile.Provider: "VELIS_AI_GENERATION_PROVIDER", &gen.Profile.BaseURL: "VELIS_AI_GENERATION_BASE_URL",
		&gen.Profile.APIKey: "VELIS_AI_GENERATION_API_KEY", &gen.Profile.Model: "VELIS_AI_GENERATION_MODEL",
		&gen.Profile.ProfileVersion: "VELIS_AI_GENERATION_PROFILE_VERSION", &gen.Profile.TimeoutRaw: "VELIS_AI_GENERATION_TIMEOUT",
		&gen.Profile.BudgetRaw: "VELIS_AI_GENERATION_BUDGET", &gen.WorkflowVersion: "VELIS_AI_GENERATION_WORKFLOW_VERSION",
		&gen.PromptVersion: "VELIS_AI_GENERATION_PROMPT_VERSION", &gen.BackoffMinRaw: "VELIS_AI_GENERATION_BACKOFF_MIN",
		&gen.BackoffMaxRaw: "VELIS_AI_GENERATION_BACKOFF_MAX", &embed.Profile.Provider: "VELIS_AI_EMBEDDING_PROVIDER",
		&embed.Profile.BaseURL: "VELIS_AI_EMBEDDING_BASE_URL", &embed.Profile.APIKey: "VELIS_AI_EMBEDDING_API_KEY",
		&embed.Profile.Model: "VELIS_AI_EMBEDDING_MODEL", &embed.Profile.ProfileVersion: "VELIS_AI_EMBEDDING_PROFILE_VERSION",
		&embed.Profile.TimeoutRaw: "VELIS_AI_EMBEDDING_TIMEOUT", &embed.Profile.BudgetRaw: "VELIS_AI_EMBEDDING_BUDGET",
		&embed.InputVersion: "VELIS_AI_EMBEDDING_INPUT_VERSION", &embed.BackoffMinRaw: "VELIS_AI_EMBEDDING_BACKOFF_MIN",
		&embed.BackoffMaxRaw: "VELIS_AI_EMBEDDING_BACKOFF_MAX", &cfg.AI.Worker.LeaseRaw: "VELIS_AI_WORKER_LEASE",
		&cfg.AI.Worker.PollRaw: "VELIS_AI_WORKER_POLL_INTERVAL",
	} {
		setString(target, key)
	}
	for target, key := range map[*int]string{
		&gen.SingleInputChars: "VELIS_AI_GENERATION_SINGLE_INPUT_CHARS", &gen.ChunkChars: "VELIS_AI_GENERATION_CHUNK_CHARS",
		&gen.MaxChunks: "VELIS_AI_GENERATION_MAX_CHUNKS", &gen.ChunkConcurrency: "VELIS_AI_GENERATION_CHUNK_CONCURRENCY",
		&gen.MaxCalls: "VELIS_AI_GENERATION_MAX_CALLS", &gen.MaxAttempts: "VELIS_AI_GENERATION_MAX_ATTEMPTS",
		&gen.MaxOutputTokens: "VELIS_AI_GENERATION_MAX_OUTPUT_TOKENS", &gen.AuditTokenBudget: "VELIS_AI_GENERATION_AUDIT_TOKEN_BUDGET",
		&gen.SummaryMaxChars: "VELIS_AI_GENERATION_SUMMARY_MAX_CHARS", &gen.KeywordMaxCount: "VELIS_AI_GENERATION_KEYWORD_MAX_COUNT",
		&gen.TopicMaxCount: "VELIS_AI_GENERATION_TOPIC_MAX_COUNT", &gen.LabelMaxChars: "VELIS_AI_GENERATION_LABEL_MAX_CHARS",
		&embed.Dimensions: "VELIS_AI_EMBEDDING_DIMENSIONS", &embed.MaxInputChars: "VELIS_AI_EMBEDDING_MAX_INPUT_CHARS",
		&embed.MaxAttempts: "VELIS_AI_EMBEDDING_MAX_ATTEMPTS", &embed.AuditTokenBudget: "VELIS_AI_EMBEDDING_AUDIT_TOKEN_BUDGET",
		&cfg.AI.Worker.BatchSize: "VELIS_AI_WORKER_BATCH_SIZE",
	} {
		if err := setInt(target, key); err != nil {
			return err
		}
	}
	return nil
}

func validateAIConfig(ai *AIConfig) error {
	var err error
	if err = validateProfile("ai.generation", &ai.Generation.Profile, false); err != nil {
		return err
	}
	if err = validateProfile("ai.embedding", &ai.Embedding.Profile, true); err != nil {
		return err
	}
	ai.Worker.Lease, err = durationWithin("ai.worker.lease", ai.Worker.LeaseRaw, 10*time.Second, 10*time.Minute)
	if err != nil {
		return err
	}
	ai.Worker.Poll, err = durationWithin("ai.worker.poll_interval", ai.Worker.PollRaw, 100*time.Millisecond, time.Minute)
	if err != nil {
		return err
	}
	if ai.Worker.BatchSize < 1 || ai.Worker.BatchSize > 100 {
		return errors.New("ai.worker.batch_size 必须介于 1 和 100")
	}
	gen := &ai.Generation
	if gen.BackoffMin, err = durationWithin("ai.generation.backoff_min", gen.BackoffMinRaw, time.Second, time.Hour); err != nil {
		return err
	}
	if gen.BackoffMax, err = durationWithin("ai.generation.backoff_max", gen.BackoffMaxRaw, time.Second, time.Hour); err != nil {
		return err
	}
	if gen.BackoffMin > gen.BackoffMax {
		return errors.New("ai.generation.backoff_min 不得大于 backoff_max")
	}
	if gen.SingleInputChars < 1000 || gen.SingleInputChars > 100000 || gen.ChunkChars < 500 || gen.ChunkChars > gen.SingleInputChars || gen.MaxChunks < 1 || gen.MaxChunks > 64 || gen.ChunkConcurrency < 1 || gen.ChunkConcurrency > 8 || gen.MaxCalls < 1 || gen.MaxCalls > 65 || gen.MaxCalls < gen.MaxChunks+1 || gen.MaxAttempts < 1 || gen.MaxAttempts > 5 || gen.MaxOutputTokens < 64 || gen.MaxOutputTokens > 8192 || gen.AuditTokenBudget < gen.MaxOutputTokens || gen.AuditTokenBudget > 1000000 || gen.SummaryMaxChars < 1 || gen.SummaryMaxChars > 4000 || gen.KeywordMaxCount < 1 || gen.KeywordMaxCount > 12 || gen.TopicMaxCount < 1 || gen.TopicMaxCount > 5 || gen.LabelMaxChars < 1 || gen.LabelMaxChars > 128 {
		return errors.New("ai.generation 的输入、分块、调用、尝试、输出或审计预算超出允许边界")
	}
	if strings.TrimSpace(gen.WorkflowVersion) == "" || strings.TrimSpace(gen.PromptVersion) == "" {
		return errors.New("ai.generation workflow_version 与 prompt_version 不能为空")
	}
	embed := &ai.Embedding
	if embed.BackoffMin, err = durationWithin("ai.embedding.backoff_min", embed.BackoffMinRaw, time.Second, time.Hour); err != nil {
		return err
	}
	if embed.BackoffMax, err = durationWithin("ai.embedding.backoff_max", embed.BackoffMaxRaw, time.Second, time.Hour); err != nil {
		return err
	}
	if embed.BackoffMin > embed.BackoffMax || embed.MaxAttempts < 1 || embed.MaxAttempts > 5 || embed.MaxInputChars < 1000 || embed.MaxInputChars > 100000 || embed.AuditTokenBudget < 1 || embed.AuditTokenBudget > 1000000 {
		return errors.New("ai.embedding 的输入、尝试、退避或审计预算超出允许边界")
	}
	if strings.TrimSpace(embed.InputVersion) == "" {
		return errors.New("ai.embedding.input_version 不能为空")
	}
	if embed.Profile.Enabled() && (embed.Dimensions < 1 || embed.Dimensions > 65536) {
		return errors.New("启用 ai.embedding 时 dimensions 必须介于 1 和 65536")
	}
	return nil
}

func validateProfile(name string, profile *ModelProfileConfig, dimensionsRequired bool) error {
	if !profile.Enabled() {
		return nil
	}
	if strings.TrimSpace(profile.Provider) == "" || strings.TrimSpace(profile.BaseURL) == "" || strings.TrimSpace(profile.APIKey) == "" || strings.TrimSpace(profile.Model) == "" || strings.TrimSpace(profile.ProfileVersion) == "" {
		return fmt.Errorf("%s 已部分配置：provider、base_url、api_key、model 与 profile_version 必须同时提供", name)
	}
	parsed, err := url.Parse(profile.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s.base_url 必须是无凭据、查询和片段的完整 HTTP(S) URL", name)
	}
	profile.Timeout, err = durationWithin(name+".timeout", profile.TimeoutRaw, time.Second, 5*time.Minute)
	if err != nil {
		return err
	}
	profile.Budget, err = durationWithin(name+".budget", profile.BudgetRaw, profile.Timeout, 15*time.Minute)
	if err != nil {
		return err
	}
	_ = dimensionsRequired
	return nil
}

// RedactedModelEndpoint 仅返回可安全记录的协议和主机。
func RedactedModelEndpoint(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "invalid"
	}
	return parsed.Scheme + "://" + parsed.Host
}
