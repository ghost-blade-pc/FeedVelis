package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadYAMLAndEnvironmentOverride(t *testing.T) {
	t.Setenv("VELIS_HTTP_ADDRESS", "127.0.0.1:9090")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte("app:\n  name: test-velis\nhttp:\n  shutdown_timeout: 3s\ndatabase:\n  connect_timeout: 2s\nworker:\n  heartbeat_interval: 1s\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.App.Name != "test-velis" {
		t.Fatalf("App.Name = %q", cfg.App.Name)
	}
	if cfg.HTTP.Address != "127.0.0.1:9090" {
		t.Fatalf("HTTP.Address = %q", cfg.HTTP.Address)
	}
	if cfg.HTTP.ShutdownTimeout != 3*time.Second {
		t.Fatalf("ShutdownTimeout = %v", cfg.HTTP.ShutdownTimeout)
	}
}

func TestValidateRejectsInvalidConnectionLimits(t *testing.T) {
	cfg := Default()
	cfg.Database.MinConnections = 10
	cfg.Database.MaxConnections = 5

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() expected error")
	}
}

func TestAsyncConfigEnvironmentAndValidation(t *testing.T) {
	t.Setenv("VELIS_RABBITMQ_URL", "amqps://secret-user:secret-pass@mq.internal:5671/velis?token=hidden")
	t.Setenv("VELIS_RELAY_BATCH_SIZE", "25")
	t.Setenv("VELIS_CONSUMER_PREFETCH", "8")
	t.Setenv("VELIS_WORKER_METRICS_ADDRESS", "0.0.0.0:9191")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Relay.BatchSize != 25 || cfg.Consumer.Prefetch != 8 || cfg.Worker.MetricsAddress != "0.0.0.0:9191" {
		t.Fatalf("异步环境覆盖失败: %+v", cfg)
	}
	redacted := RedactedRabbitMQURL(cfg.RabbitMQ.URL)
	if redacted != "amqps://mq.internal:5671/velis" || strings.Contains(redacted, "secret") || strings.Contains(redacted, "hidden") {
		t.Fatalf("MQ URL 脱敏失败: %q", redacted)
	}
}

func TestDisabledRabbitMQIgnoresConnectionSpecificConfig(t *testing.T) {
	cfg := Default()
	cfg.RabbitMQ.URL = ""
	cfg.Relay.LeaseRaw = "not-a-duration"
	cfg.Consumer.Prefetch = -1
	if err := cfg.Validate(); err != nil {
		t.Fatalf("MQ 关闭时不应校验连接专用配置: %v", err)
	}
}

func TestEnabledRabbitMQRejectsInvalidBounds(t *testing.T) {
	tests := []func(*Config){
		func(cfg *Config) { cfg.Relay.BatchSize = 0 },
		func(cfg *Config) { cfg.Relay.LeaseRaw = "5s" },
		func(cfg *Config) { cfg.Consumer.Prefetch = 257 },
	}
	for index, mutate := range tests {
		cfg := Default()
		cfg.RabbitMQ.URL = "amqp://guest:guest@localhost:5672/"
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Fatalf("case %d 应拒绝", index)
		}
	}
}

func TestContentConfigPriorityAndEnvironmentOverrides(t *testing.T) {
	t.Setenv("VELIS_ASSET_ENDPOINT", "minio.internal:9443")
	t.Setenv("VELIS_ASSET_UPLOAD_ENDPOINT", "https://assets.example.com")
	t.Setenv("VELIS_ASSET_BUCKET", "env-assets")
	t.Setenv("VELIS_ASSET_ACCESS_KEY", "env-access")
	t.Setenv("VELIS_ASSET_SECRET_KEY", "env-secret")
	t.Setenv("VELIS_ASSET_USE_TLS", "true")
	t.Setenv("VELIS_ASSET_WEB_ORIGIN", "https://velis.example.com")
	t.Setenv("VELIS_ASSET_USER_QUOTA_BYTES", "536870912")
	t.Setenv("VELIS_IDEMPOTENCY_RETENTION", "48h")
	t.Setenv("VELIS_FEED_PROXY_URL", "http://proxy-user:proxy-pass@proxy.internal:3128")
	t.Setenv("VELIS_APP_ENVIRONMENT", "production")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte("assets:\n  endpoint: yaml-minio:9000\n  upload_endpoint: https://yaml-assets.example.com\n  bucket: yaml-assets\n  access_key: yaml-access\n  secret_key: yaml-secret\n  web_origin: https://yaml.example.com\n  user_quota_bytes: 268435456\nidempotency:\n  retention: 12h\nfeed:\n  proxy_url: http://yaml-proxy:3128\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Assets.Endpoint != "minio.internal:9443" || cfg.Assets.Bucket != "env-assets" || !cfg.Assets.UseTLS {
		t.Fatalf("资产环境覆盖失败: %+v", cfg.Assets)
	}
	if cfg.Assets.UploadEndpoint != "https://assets.example.com" {
		t.Fatalf("公共上传端点环境覆盖失败: %q", cfg.Assets.UploadEndpoint)
	}
	if cfg.Assets.UserQuotaBytes != 512*1024*1024 || cfg.Idempotency.Retention != 48*time.Hour {
		t.Fatalf("额度/幂等保留期 = %d/%v", cfg.Assets.UserQuotaBytes, cfg.Idempotency.Retention)
	}
	if cfg.Feed.ProxyURL != "http://proxy-user:proxy-pass@proxy.internal:3128" {
		t.Fatalf("Feed 代理环境覆盖失败: %q", cfg.Feed.ProxyURL)
	}
}

func TestAssetUploadEndpointValidation(t *testing.T) {
	valid := []string{"http://localhost:9000", "https://assets.example.com", "https://assets.example.com/"}
	for _, endpoint := range valid {
		t.Run("允许_"+endpoint, func(t *testing.T) {
			cfg := validAssetConfig()
			cfg.Assets.UploadEndpoint = endpoint
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}

	invalid := []string{
		"", "localhost:9000", "ftp://assets.example.com", "https:///missing-host",
		"https://user:pass@assets.example.com", "https://assets.example.com/prefix",
		"https://assets.example.com?token=secret", "https://assets.example.com/#fragment",
	}
	for _, endpoint := range invalid {
		t.Run("拒绝_"+endpoint, func(t *testing.T) {
			cfg := validAssetConfig()
			cfg.Assets.UploadEndpoint = endpoint
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "assets.upload_endpoint") {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}

	// 未启用资产时不要求公共上传端点，保持局部降级能力。
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("未启用资产时 Validate() error = %v", err)
	}
}

func validAssetConfig() Config {
	cfg := Default()
	cfg.Assets.Endpoint = "minio.internal:9000"
	cfg.Assets.UploadEndpoint = "https://assets.example.com"
	cfg.Assets.AccessKey = "access"
	cfg.Assets.SecretKey = "secret"
	cfg.Assets.WebOrigin = "https://velis.example.com"
	return cfg
}

func TestContentConfigRejectsInvalidBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"单文件超过 10 MiB", func(cfg *Config) { cfg.Assets.MaxFileBytes = 10*1024*1024 + 1 }, "max_file_bytes"},
		{"宽度超过限制", func(cfg *Config) { cfg.Assets.MaxWidth = 8193 }, "max_width"},
		{"像素超过限制", func(cfg *Config) { cfg.Assets.MaxPixels = 40_000_001 }, "max_pixels"},
		{"pending 超过限制", func(cfg *Config) { cfg.Assets.PendingLimit = 21 }, "pending_limit"},
		{"幂等期限过短", func(cfg *Config) { cfg.Idempotency.RetentionRaw = "59m" }, "idempotency.retention"},
		{"上传期限过长", func(cfg *Config) { cfg.Assets.UploadRaw = "61m" }, "assets.upload_ttl"},
		{"代理协议非法", func(cfg *Config) { cfg.Feed.ProxyURL = "socks5://proxy.internal:1080" }, "feed.proxy_url"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v，期望包含 %q", err, test.want)
			}
		})
	}
}

func TestConfigLogValueRedactsSensitiveValues(t *testing.T) {
	cfg := Default()
	cfg.Database.URL = "postgres://secret-user:secret-db-password@localhost:5432/velis?sslmode=disable"
	cfg.Assets.Endpoint = "minio.internal:9000"
	cfg.Assets.UploadEndpoint = "https://assets.example.com"
	cfg.Assets.Bucket = "private-assets"
	cfg.Assets.AccessKey = "secret-access"
	cfg.Assets.SecretKey = "secret-object-password"
	cfg.Assets.WebOrigin = "http://localhost:5173"
	cfg.Feed.ProxyURL = "http://proxy-user:secret-proxy-password@proxy.internal:3128"
	cfg.AI.Generation.Profile.Provider = "test"
	cfg.AI.Generation.Profile.BaseURL = "https://models.internal/v1"
	cfg.AI.Generation.Profile.APIKey = "secret-model-key"
	cfg.AI.Generation.Profile.Model = "chat-model"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("测试配置无效: %v", err)
	}

	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	logger.Info("配置已加载", "config", cfg)
	logged := output.String()
	for _, secret := range []string{"secret-user", "secret-db-password", "secret-access", "secret-object-password", "proxy-user", "secret-proxy-password", "secret-model-key"} {
		if strings.Contains(logged, secret) {
			t.Errorf("日志泄露敏感值 %q: %s", secret, logged)
		}
	}
	for _, safe := range []string{"private-assets", "trusted_proxy", "assets_enabled"} {
		if !strings.Contains(logged, safe) {
			t.Errorf("日志缺少安全摘要 %q: %s", safe, logged)
		}
	}
}

func TestAIConfigDisabledByDefault(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AI.Generation.Profile.Enabled() || cfg.AI.Embedding.Profile.Enabled() {
		t.Fatal("空 profile 应禁用模型阶段")
	}
}

func TestAIConfigEnvironmentOverridesYAML(t *testing.T) {
	t.Setenv("VELIS_AI_GENERATION_PROVIDER", "env-provider")
	t.Setenv("VELIS_AI_GENERATION_BASE_URL", "https://chat.internal/v1")
	t.Setenv("VELIS_AI_GENERATION_API_KEY", "env-secret")
	t.Setenv("VELIS_AI_GENERATION_MODEL", "env-model")
	t.Setenv("VELIS_AI_GENERATION_MAX_CHUNKS", "6")
	t.Setenv("VELIS_AI_GENERATION_MAX_CALLS", "7")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte("ai:\n  generation:\n    provider: yaml-provider\n    base_url: https://yaml.invalid/v1\n    api_key: yaml-secret\n    model: yaml-model\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AI.Generation.Profile.Provider != "env-provider" || cfg.AI.Generation.Profile.Model != "env-model" || cfg.AI.Generation.MaxChunks != 6 {
		t.Fatalf("AI 环境覆盖失败: %+v", cfg.AI.Generation)
	}
}

func TestAIConfigRejectsPartialAndUnboundedProfiles(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"部分 generation profile", func(cfg *Config) { cfg.AI.Generation.Profile.Provider = "partial" }},
		{"Embedding 缺少维度", func(cfg *Config) {
			p := &cfg.AI.Embedding.Profile
			p.Provider = "test"
			p.BaseURL = "https://embed.internal/v1"
			p.APIKey = "key"
			p.Model = "embed"
		}},
		{"分块无界", func(cfg *Config) { cfg.AI.Generation.MaxChunks = 65; cfg.AI.Generation.MaxCalls = 66 }},
		{"尝试无界", func(cfg *Config) { cfg.AI.Embedding.MaxAttempts = 6 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("应拒绝非法 AI 配置")
			}
		})
	}
}
