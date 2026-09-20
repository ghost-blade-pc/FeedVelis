package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func secret(fill byte, size int) string {
	raw := make([]byte, size)
	for i := range raw {
		raw[i] = fill
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func enabledConfig() Config {
	cfg := Default()
	cfg.App.Environment = "production"
	cfg.Auth.Enabled = true
	cfg.Auth.JWTActiveKID = "k1"
	cfg.Auth.JWTActiveKey = secret(1, 32)
	cfg.Auth.AllowedOrigin = "https://velis.example.com"
	cfg.Auth.ThrottleKey = secret(2, 32)
	return cfg
}

func TestDefaultAuthConfigIsDisabledAndValid(t *testing.T) {
	cfg := Default()
	if cfg.Auth.Enabled || cfg.Auth.RegistrationEnabled {
		t.Fatal("注册与认证默认必须关闭")
	}
	if !cfg.Auth.CookieSecure {
		t.Fatal("Cookie 默认必须 Secure")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("默认配置应有效: %v", err)
	}
	policy := cfg.Auth.Policy()
	if policy.AccountLimit != 5 || policy.IPLimit != 30 || policy.BlockDuration != 15*time.Minute {
		t.Fatalf("默认限流策略 = %+v", policy)
	}
}

func TestEnabledAuthConfig(t *testing.T) {
	cfg := enabledConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("完整配置应有效: %v", err)
	}
	if cfg.Auth.AccessTTL != 15*time.Minute || cfg.Auth.SessionTTL != 168*time.Hour {
		t.Fatalf("会话期限 = %v/%v", cfg.Auth.AccessTTL, cfg.Auth.SessionTTL)
	}
	key, err := cfg.Auth.ActiveSigningKey()
	if err != nil || len(key.Key) != 32 || key.KID != "k1" {
		t.Fatalf("主动密钥 = %+v err=%v", key, err)
	}
	if previous, err := cfg.Auth.PreviousSigningKey(); err != nil || previous != nil {
		t.Fatalf("未配置上一把密钥时应为 nil: %+v err=%v", previous, err)
	}
}

func TestAuthConfigRejectsMissingOrWeakSecrets(t *testing.T) {
	cases := map[string]func(*Config){
		"缺少主动密钥":       func(c *Config) { c.Auth.JWTActiveKey = "" },
		"主动密钥过短":       func(c *Config) { c.Auth.JWTActiveKey = secret(1, 16) },
		"主动密钥非 Base64": func(c *Config) { c.Auth.JWTActiveKey = "不是密钥" },
		"缺少 kid":       func(c *Config) { c.Auth.JWTActiveKID = "" },
		"kid 含非法字符":    func(c *Config) { c.Auth.JWTActiveKID = "bad kid" },
		"缺少 issuer":    func(c *Config) { c.Auth.JWTIssuer = "" },
		"缺少限流密钥":       func(c *Config) { c.Auth.ThrottleKey = "" },
		"限流密钥过短":       func(c *Config) { c.Auth.ThrottleKey = secret(2, 16) },
		"上一把密钥缺少 kid":  func(c *Config) { c.Auth.JWTPreviousKey = secret(3, 32) },
		"上一把密钥缺少密钥":    func(c *Config) { c.Auth.JWTPreviousKID = "k0" },
		"重复 kid": func(c *Config) {
			c.Auth.JWTPreviousKID = "k1"
			c.Auth.JWTPreviousKey = secret(3, 32)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := enabledConfig()
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("%s 必须被拒绝", name)
			}
		})
	}
}

func TestAuthConfigRejectsInvalidDurations(t *testing.T) {
	for _, access := range []string{"4m", "31m", "0s", "abc"} {
		cfg := enabledConfig()
		cfg.Auth.AccessRaw = access
		if err := cfg.Validate(); err == nil {
			t.Fatalf("auth.access_ttl=%s 必须被拒绝", access)
		}
	}
	for _, session := range []string{"30m", "721h"} {
		cfg := enabledConfig()
		cfg.Auth.SessionRaw = session
		if err := cfg.Validate(); err == nil {
			t.Fatalf("auth.session_ttl=%s 必须被拒绝", session)
		}
	}
	cfg := enabledConfig()
	cfg.Auth.CleanupRaw = "0s"
	if err := cfg.Validate(); err == nil {
		t.Fatal("清理间隔必须为正")
	}
	cfg = enabledConfig()
	cfg.Auth.CleanupBatch = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("清理批量必须为正")
	}
}

func TestAuthConfigRejectsNonExactOrigin(t *testing.T) {
	for _, origin := range []string{
		"", "*", "https://*.example.com", "https://example.com/app", "https://example.com:443",
		"ftp://example.com", "example.com", "https://user@example.com",
	} {
		cfg := enabledConfig()
		cfg.Auth.AllowedOrigin = origin
		if err := cfg.Validate(); err == nil {
			t.Fatalf("来源 %q 必须被拒绝", origin)
		}
	}
	cfg := enabledConfig()
	cfg.Auth.AllowedOrigin = "https://velis.example.com:8443"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("非默认端口的 https 来源应有效: %v", err)
	}
}

func TestAuthConfigCookieSecureRules(t *testing.T) {
	cfg := enabledConfig()
	cfg.Auth.CookieSecure = false
	if err := cfg.Validate(); err == nil {
		t.Fatal("非 development 环境关闭 Secure 必须被拒绝")
	}

	cfg = enabledConfig()
	cfg.App.Environment = "development"
	cfg.Auth.CookieSecure = false
	cfg.Auth.AllowedOrigin = "http://example.com"
	if err := cfg.Validate(); err == nil {
		t.Fatal("关闭 Secure 时来源必须是本机")
	}

	cfg = enabledConfig()
	cfg.App.Environment = "development"
	cfg.Auth.CookieSecure = false
	cfg.Auth.AllowedOrigin = "http://localhost:5173"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("本地开发配置应有效: %v", err)
	}

	cfg = enabledConfig()
	cfg.App.Environment = "development"
	cfg.Auth.AllowedOrigin = "http://127.0.0.1:5173"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("本地开发来源应有效: %v", err)
	}
}

func TestAuthConfigRejectsInvalidThrottleAndProxyConfig(t *testing.T) {
	cfg := enabledConfig()
	cfg.Auth.Throttle.AccountLimit = 40
	if err := cfg.Validate(); err == nil {
		t.Fatal("账号阈值大于 IP 阈值必须被拒绝")
	}
	cfg = enabledConfig()
	cfg.Auth.Throttle.AccountWindowRaw = "30m"
	if err := cfg.Validate(); err == nil {
		t.Fatal("账号窗口长于 IP 窗口必须被拒绝")
	}
	cfg = enabledConfig()
	cfg.Auth.Throttle.AccountLimit = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("零阈值必须被拒绝")
	}
	for _, params := range []func(*Config){
		func(c *Config) { c.Auth.Argon2.MemoryKiB = 1024 },
		func(c *Config) { c.Auth.Argon2.Iterations = 0 },
		func(c *Config) { c.Auth.Argon2.Parallelism = 8 },
		func(c *Config) { c.Auth.Argon2.Concurrency = 0 },
	} {
		cfg = enabledConfig()
		params(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Fatal("超预算散列参数必须被拒绝")
		}
	}
	cfg = enabledConfig()
	cfg.Auth.TrustedProxyCIDRs = []string{"10.0.0.0/8", "not-a-cidr"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("无效网段必须被拒绝")
	}
	cfg = enabledConfig()
	cfg.Auth.TrustedProxyCIDRs = []string{"172.18.0.0/16"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("合法网段应通过: %v", err)
	}
}

func TestAuthEnvironmentOverrides(t *testing.T) {
	t.Setenv("VELIS_AUTH_ENABLED", "true")
	t.Setenv("VELIS_AUTH_REGISTRATION_ENABLED", "true")
	t.Setenv("VELIS_AUTH_JWT_ACTIVE_KID", "env-key")
	t.Setenv("VELIS_AUTH_JWT_ACTIVE_KEY", secret(4, 32))
	t.Setenv("VELIS_AUTH_ALLOWED_ORIGIN", "https://velis.example.com")
	t.Setenv("VELIS_AUTH_THROTTLE_KEY", secret(5, 32))
	t.Setenv("VELIS_AUTH_ACCESS_TTL", "10m")
	t.Setenv("VELIS_AUTH_SESSION_TTL", "24h")
	t.Setenv("VELIS_AUTH_COOKIE_SECURE", "true")
	t.Setenv("VELIS_AUTH_THROTTLE_ACCOUNT_LIMIT", "3")
	t.Setenv("VELIS_AUTH_THROTTLE_IP_LIMIT", "12")
	t.Setenv("VELIS_AUTH_TRUSTED_PROXY_CIDRS", "172.18.0.0/16, 10.1.0.0/16")
	t.Setenv("VELIS_AUTH_ARGON2_MEMORY_KIB", "32768")
	t.Setenv("VELIS_APP_ENVIRONMENT", "production")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("环境变量配置应有效: %v", err)
	}
	if !cfg.Auth.Enabled || !cfg.Auth.RegistrationEnabled {
		t.Fatal("开关未从环境变量读取")
	}
	if cfg.Auth.AccessTTL != 10*time.Minute || cfg.Auth.SessionTTL != 24*time.Hour {
		t.Fatalf("期限 = %v/%v", cfg.Auth.AccessTTL, cfg.Auth.SessionTTL)
	}
	if cfg.Auth.JWTActiveKID != "env-key" || cfg.Auth.Argon2.MemoryKiB != 32768 {
		t.Fatalf("kid/内存 = %s/%d", cfg.Auth.JWTActiveKID, cfg.Auth.Argon2.MemoryKiB)
	}
	if len(cfg.Auth.TrustedProxyCIDRs) != 2 || cfg.Auth.TrustedProxyCIDRs[1] != "10.1.0.0/16" {
		t.Fatalf("可信代理网段 = %v", cfg.Auth.TrustedProxyCIDRs)
	}
	if policy := cfg.Auth.Policy(); policy.AccountLimit != 3 || policy.IPLimit != 12 {
		t.Fatalf("限流策略 = %+v", policy)
	}

	t.Setenv("VELIS_AUTH_COOKIE_SECURE", "not-a-bool")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "VELIS_AUTH_COOKIE_SECURE") {
		t.Fatalf("非法布尔值必须报错: %v", err)
	}
}
