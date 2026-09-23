package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"
)

// AssetConfig 描述私有文章图片存储、额度和生命周期限制。
type AssetConfig struct {
	Endpoint          string `yaml:"endpoint"`
	UploadEndpoint    string `yaml:"upload_endpoint"`
	Bucket            string `yaml:"bucket"`
	AccessKey         string `yaml:"access_key"`
	SecretKey         string `yaml:"secret_key"`
	UseTLS            bool   `yaml:"use_tls"`
	WebOrigin         string `yaml:"web_origin"`
	UploadRaw         string `yaml:"upload_ttl"`
	PendingRaw        string `yaml:"pending_ttl"`
	UnboundRaw        string `yaml:"unbound_ttl"`
	MaxFileBytes      int64  `yaml:"max_file_bytes"`
	MaxWidth          int    `yaml:"max_width"`
	MaxHeight         int    `yaml:"max_height"`
	MaxPixels         int64  `yaml:"max_pixels"`
	UserQuotaBytes    int64  `yaml:"user_quota_bytes"`
	PendingLimit      int    `yaml:"pending_limit"`
	ArticleImageLimit int    `yaml:"article_image_limit"`
	ArticleBytesLimit int64  `yaml:"article_bytes_limit"`

	UploadTTL  time.Duration `yaml:"-"`
	PendingTTL time.Duration `yaml:"-"`
	UnboundTTL time.Duration `yaml:"-"`
}

// Enabled 表示是否配置了对象存储端点。
func (c AssetConfig) Enabled() bool { return strings.TrimSpace(c.Endpoint) != "" }

// IdempotencyConfig 描述成功操作结果的保留窗口。
type IdempotencyConfig struct {
	RetentionRaw string        `yaml:"retention"`
	Retention    time.Duration `yaml:"-"`
}

// FeedConfig 描述 Feed 抓取专用可信出口代理；通用代理环境变量不在此配置中。
type FeedConfig struct {
	ProxyURL string `yaml:"proxy_url"`
}

func validateContentConfig(cfg *Config) error {
	var err error
	if cfg.Idempotency.Retention, err = durationWithin("idempotency.retention", cfg.Idempotency.RetentionRaw, time.Hour, 7*24*time.Hour); err != nil {
		return err
	}
	if err := validateFeedProxy(cfg.Feed.ProxyURL); err != nil {
		return err
	}
	assets := &cfg.Assets
	if assets.UploadTTL, err = durationWithin("assets.upload_ttl", assets.UploadRaw, time.Minute, time.Hour); err != nil {
		return err
	}
	if assets.PendingTTL, err = durationWithin("assets.pending_ttl", assets.PendingRaw, time.Hour, 7*24*time.Hour); err != nil {
		return err
	}
	if assets.UnboundTTL, err = durationWithin("assets.unbound_ttl", assets.UnboundRaw, 24*time.Hour, 30*24*time.Hour); err != nil {
		return err
	}
	if assets.PendingTTL <= assets.UploadTTL {
		return errors.New("assets.pending_ttl 必须长于 upload_ttl")
	}
	if assets.MaxFileBytes <= 0 || assets.MaxFileBytes > 10*1024*1024 {
		return errors.New("assets.max_file_bytes 必须介于 1 和 10 MiB 之间")
	}
	if assets.MaxWidth <= 0 || assets.MaxWidth > 8192 || assets.MaxHeight <= 0 || assets.MaxHeight > 8192 {
		return errors.New("assets.max_width 与 max_height 必须介于 1 和 8192 之间")
	}
	if assets.MaxPixels <= 0 || assets.MaxPixels > 40_000_000 {
		return errors.New("assets.max_pixels 必须介于 1 和 40000000 之间")
	}
	if assets.UserQuotaBytes < assets.MaxFileBytes || assets.UserQuotaBytes > 1024*1024*1024 {
		return errors.New("assets.user_quota_bytes 必须不小于单文件限制且不超过 1 GiB")
	}
	if assets.PendingLimit <= 0 || assets.PendingLimit > 20 {
		return errors.New("assets.pending_limit 必须介于 1 和 20 之间")
	}
	if assets.ArticleImageLimit <= 0 || assets.ArticleImageLimit > 20 {
		return errors.New("assets.article_image_limit 必须介于 1 和 20 之间")
	}
	if assets.ArticleBytesLimit < assets.MaxFileBytes || assets.ArticleBytesLimit > 50*1024*1024 {
		return errors.New("assets.article_bytes_limit 必须不小于单文件限制且不超过 50 MiB")
	}
	if !assets.Enabled() {
		return nil
	}
	if _, _, err := net.SplitHostPort(assets.Endpoint); err != nil {
		return fmt.Errorf("assets.endpoint 必须是 host:port: %w", err)
	}
	if err := validateUploadEndpoint(assets.UploadEndpoint); err != nil {
		return fmt.Errorf("assets.upload_endpoint 无效: %w", err)
	}
	if strings.TrimSpace(assets.Bucket) == "" || strings.TrimSpace(assets.AccessKey) == "" || strings.TrimSpace(assets.SecretKey) == "" {
		return errors.New("配置 assets.endpoint 时 bucket、access_key 与 secret_key 不能为空")
	}
	if err := validateAllowedOrigin(assets.WebOrigin, cfg.App.Environment); err != nil {
		return fmt.Errorf("assets.web_origin 无效: %w", err)
	}
	return nil
}

func validateUploadEndpoint(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("启用对象存储时不能为空")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Opaque != "" {
		return errors.New("必须是完整的 http 或 https URL")
	}
	if parsed.User != nil {
		return errors.New("不得包含用户信息")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return errors.New("不得包含查询或片段")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return errors.New("不得包含路径前缀")
	}
	return nil
}

func validateFeedProxy(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("feed.proxy_url 无效: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("feed.proxy_url 必须是完整的 http 或 https URL")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("feed.proxy_url 不得包含查询或片段")
	}
	return nil
}

// LogValue 只暴露可安全记录的运行模式和限制，避免数据库、对象存储及代理凭据进入日志。
func (cfg Config) LogValue() slog.Value {
	proxyMode := "direct"
	if strings.TrimSpace(cfg.Feed.ProxyURL) != "" {
		proxyMode = "trusted_proxy"
	}
	return slog.GroupValue(
		slog.String("environment", cfg.App.Environment),
		slog.Bool("auth_enabled", cfg.Auth.Enabled),
		slog.Bool("assets_enabled", cfg.Assets.Enabled()),
		slog.String("asset_bucket", cfg.Assets.Bucket),
		slog.String("feed_network_mode", proxyMode),
		slog.Bool("rabbitmq_enabled", strings.TrimSpace(cfg.RabbitMQ.URL) != ""),
		slog.String("rabbitmq_endpoint", RedactedRabbitMQURL(cfg.RabbitMQ.URL)),
		slog.Duration("idempotency_retention", cfg.Idempotency.Retention),
	)
}
