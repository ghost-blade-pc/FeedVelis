package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/security"
)

// AuthConfig 是认证相关配置；auth.enabled 为 false 时不注册认证路由，保持原有匿名行为。
type AuthConfig struct {
	Enabled             bool     `yaml:"enabled"`
	RegistrationEnabled bool     `yaml:"registration_enabled"`
	AccessRaw           string   `yaml:"access_ttl"`
	SessionRaw          string   `yaml:"session_ttl"`
	JWTIssuer           string   `yaml:"jwt_issuer"`
	JWTAudience         string   `yaml:"jwt_audience"`
	JWTActiveKID        string   `yaml:"jwt_active_kid"`
	JWTActiveKey        string   `yaml:"jwt_active_key"`
	JWTPreviousKID      string   `yaml:"jwt_previous_kid"`
	JWTPreviousKey      string   `yaml:"jwt_previous_key"`
	AllowedOrigin       string   `yaml:"allowed_origin"`
	CookieSecure        bool     `yaml:"cookie_secure"`
	ThrottleKey         string   `yaml:"throttle_key"`
	TrustedProxyCIDRs   []string `yaml:"trusted_proxy_cidrs"`
	CleanupRaw          string   `yaml:"cleanup_interval"`
	CleanupBatch        int      `yaml:"cleanup_batch"`

	AccessTTL       time.Duration  `yaml:"-"`
	SessionTTL      time.Duration  `yaml:"-"`
	CleanupInterval time.Duration  `yaml:"-"`
	Throttle        ThrottleConfig `yaml:"throttle"`
	Argon2          Argon2Config   `yaml:"argon2"`
}

// ThrottleConfig 是登录失败限流参数；默认值见 Default()。
type ThrottleConfig struct {
	AccountWindowRaw string        `yaml:"account_window"`
	AccountLimit     int           `yaml:"account_limit"`
	IPWindowRaw      string        `yaml:"ip_window"`
	IPLimit          int           `yaml:"ip_limit"`
	BlockRaw         string        `yaml:"block_duration"`
	AccountWindow    time.Duration `yaml:"-"`
	IPWindow         time.Duration `yaml:"-"`
	BlockDuration    time.Duration `yaml:"-"`
}

// Argon2Config 是密码散列参数；并发额度为每个 API 进程同时执行的散列数。
type Argon2Config struct {
	MemoryKiB   uint32 `yaml:"memory_kib"`
	Iterations  uint32 `yaml:"iterations"`
	Parallelism uint8  `yaml:"parallelism"`
	SaltBytes   int    `yaml:"salt_bytes"`
	KeyBytes    int    `yaml:"key_bytes"`
	Concurrency int    `yaml:"concurrency"`
}

const (
	authMinAccessTTL    = 5 * time.Minute
	authMaxAccessTTL    = 30 * time.Minute
	authMinSessionTTL   = time.Hour
	authMaxSessionTTL   = 720 * time.Hour
	authMaxCleanupBatch = 5000
	authMinSecretBytes  = 32
)

// Policy 返回登录限流策略，供 Application 直接使用。
func (c AuthConfig) Policy() accountDomain.ThrottlePolicy {
	return accountDomain.ThrottlePolicy{
		AccountWindow: c.Throttle.AccountWindow,
		AccountLimit:  c.Throttle.AccountLimit,
		IPWindow:      c.Throttle.IPWindow,
		IPLimit:       c.Throttle.IPLimit,
		BlockDuration: c.Throttle.BlockDuration,
	}
}

// Argon2Params 返回散列参数。
func (c AuthConfig) Argon2Params() security.Argon2Params {
	return security.Argon2Params{
		MemoryKiB:   c.Argon2.MemoryKiB,
		Iterations:  c.Argon2.Iterations,
		Parallelism: c.Argon2.Parallelism,
		SaltBytes:   c.Argon2.SaltBytes,
		KeyBytes:    c.Argon2.KeyBytes,
	}
}

// ActiveSigningKey 解码主动签名密钥。
func (c AuthConfig) ActiveSigningKey() (security.SigningKey, error) {
	key, err := decodeSecret(c.JWTActiveKey)
	if err != nil {
		return security.SigningKey{}, fmt.Errorf("auth.jwt_active_key 无效: %w", err)
	}
	return security.SigningKey{KID: c.JWTActiveKID, Key: key}, nil
}

// PreviousSigningKey 在配置了上一把密钥时返回它，否则返回 nil。
func (c AuthConfig) PreviousSigningKey() (*security.SigningKey, error) {
	if strings.TrimSpace(c.JWTPreviousKID) == "" && strings.TrimSpace(c.JWTPreviousKey) == "" {
		return nil, nil
	}
	key, err := decodeSecret(c.JWTPreviousKey)
	if err != nil {
		return nil, fmt.Errorf("auth.jwt_previous_key 无效: %w", err)
	}
	return &security.SigningKey{KID: c.JWTPreviousKID, Key: key}, nil
}

// ThrottleKeyBytes 解码限流查找键的配置密钥。
func (c AuthConfig) ThrottleKeyBytes() ([]byte, error) {
	key, err := decodeSecret(c.ThrottleKey)
	if err != nil {
		return nil, fmt.Errorf("auth.throttle_key 无效: %w", err)
	}
	return key, nil
}

// validateAuth 解析并校验认证配置；解析结果写回 cfg，因此必须使用指针。
func validateAuth(cfg *AuthConfig, environment string) error {
	if err := validateThrottleConfig(&cfg.Throttle); err != nil {
		return err
	}
	if err := security.ValidateArgon2Params(cfg.Argon2Params()); err != nil {
		return err
	}
	if cfg.Argon2.Concurrency < 1 {
		return errors.New("auth.argon2.concurrency 必须大于 0")
	}
	// 关闭认证时不注册认证路由，其余认证配置不参与校验。
	if !cfg.Enabled {
		return nil
	}
	var err error
	if cfg.AccessTTL, err = durationWithin("auth.access_ttl", cfg.AccessRaw, authMinAccessTTL, authMaxAccessTTL); err != nil {
		return err
	}
	if cfg.SessionTTL, err = durationWithin("auth.session_ttl", cfg.SessionRaw, authMinSessionTTL, authMaxSessionTTL); err != nil {
		return err
	}
	if cfg.CleanupInterval, err = positiveDuration("auth.cleanup_interval", cfg.CleanupRaw); err != nil {
		return err
	}
	if cfg.CleanupBatch <= 0 || cfg.CleanupBatch > authMaxCleanupBatch {
		return fmt.Errorf("auth.cleanup_batch 必须介于 1 和 %d 之间", authMaxCleanupBatch)
	}
	if strings.TrimSpace(cfg.JWTIssuer) == "" || strings.TrimSpace(cfg.JWTAudience) == "" {
		return errors.New("auth.jwt_issuer 与 auth.jwt_audience 在启用认证时不能为空")
	}
	active, err := cfg.ActiveSigningKey()
	if err != nil {
		return err
	}
	previous, err := cfg.PreviousSigningKey()
	if err != nil {
		return err
	}
	if _, err := security.NewJWTSigner(cfg.JWTIssuer, cfg.JWTAudience, active, previous, 0, nil); err != nil {
		return fmt.Errorf("auth JWT 配置无效: %w", err)
	}
	if _, err := cfg.ThrottleKeyBytes(); err != nil {
		return err
	}
	if err := validateAllowedOrigin(cfg.AllowedOrigin, environment); err != nil {
		return err
	}
	if !cfg.CookieSecure {
		if environment != "development" {
			return errors.New("auth.cookie_secure 只允许在 development 环境关闭")
		}
		if err := requireLoopbackOrigin(cfg.AllowedOrigin); err != nil {
			return err
		}
	}
	for _, cidr := range cfg.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(strings.TrimSpace(cidr)); err != nil {
			return fmt.Errorf("auth.trusted_proxy_cidrs 含无效网段 %q: %w", cidr, err)
		}
	}
	return nil
}

func validateThrottleConfig(throttle *ThrottleConfig) error {
	var err error
	if throttle.AccountWindow, err = positiveDuration("auth.throttle.account_window", throttle.AccountWindowRaw); err != nil {
		return err
	}
	if throttle.IPWindow, err = positiveDuration("auth.throttle.ip_window", throttle.IPWindowRaw); err != nil {
		return err
	}
	if throttle.BlockDuration, err = positiveDuration("auth.throttle.block_duration", throttle.BlockRaw); err != nil {
		return err
	}
	if throttle.AccountLimit <= 0 || throttle.IPLimit <= 0 {
		return errors.New("auth.throttle 的账号与 IP 阈值必须大于 0")
	}
	if throttle.AccountLimit > throttle.IPLimit {
		return errors.New("auth.throttle 的账号阈值不能大于 IP 阈值")
	}
	if throttle.AccountWindow > throttle.IPWindow {
		return errors.New("auth.throttle 的账号窗口不能长于 IP 窗口")
	}
	return nil
}

// validateAllowedOrigin 要求精确来源：含 scheme 与 host，不带路径、查询、占位符或默认端口。
func validateAllowedOrigin(raw, environment string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return errors.New("auth.allowed_origin 在启用认证时不能为空")
	}
	if strings.ContainsAny(trimmed, "*?#") {
		return errors.New("auth.allowed_origin 必须是精确来源，不接受通配符、查询或片段")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("auth.allowed_origin 无效: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("auth.allowed_origin 必须以 http 或 https 开头")
	}
	if parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("auth.allowed_origin 必须只包含 scheme、host 和可选端口")
	}
	if port := parsed.Port(); port == "80" || port == "443" {
		return errors.New("auth.allowed_origin 不得包含默认端口")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	// 明文来源只允许本机开发使用。
	if environment != "development" {
		return errors.New("auth.allowed_origin 在非 development 环境必须是 https 来源")
	}
	return requireLoopbackOrigin(trimmed)
}

func requireLoopbackOrigin(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("auth.allowed_origin 无效: %w", err)
	}
	host := parsed.Hostname()
	if host != "localhost" && host != "127.0.0.1" {
		return errors.New("关闭 Secure 时 auth.allowed_origin 只能是 localhost 或 127.0.0.1")
	}
	return nil
}

func durationWithin(name, raw string, min, max time.Duration) (time.Duration, error) {
	d, err := positiveDuration(name, raw)
	if err != nil {
		return 0, err
	}
	if d < min || d > max {
		return 0, fmt.Errorf("%s 必须介于 %s 和 %s 之间", name, min, max)
	}
	return d, nil
}

// decodeSecret 解码 Base64 密钥，同时接受带填充与无填充的两种标准写法。
func decodeSecret(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("密钥不能为空")
	}
	var decoded []byte
	var err error
	if decoded, err = base64.StdEncoding.DecodeString(trimmed); err != nil {
		if decoded, err = base64.RawStdEncoding.DecodeString(trimmed); err != nil {
			return nil, errors.New("密钥必须是 Base64 编码")
		}
	}
	if len(decoded) < authMinSecretBytes {
		return nil, fmt.Errorf("密钥解码后不得少于 %d 字节", authMinSecretBytes)
	}
	return decoded, nil
}
