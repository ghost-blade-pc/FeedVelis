// Package config 负责加载并校验进程配置。
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"
)

// Config 是 API、Worker 和迁移命令共享的配置结构。
type Config struct {
	App         AppConfig         `yaml:"app"`
	HTTP        HTTPConfig        `yaml:"http"`
	Database    DatabaseConfig    `yaml:"database"`
	Worker      WorkerConfig      `yaml:"worker"`
	Auth        AuthConfig        `yaml:"auth"`
	Assets      AssetConfig       `yaml:"assets"`
	Idempotency IdempotencyConfig `yaml:"idempotency"`
	Feed        FeedConfig        `yaml:"feed"`
}

type AppConfig struct {
	Name        string `yaml:"name"`
	Environment string `yaml:"environment"`
	LogLevel    string `yaml:"log_level"`
}

type HTTPConfig struct {
	Address         string        `yaml:"address"`
	ShutdownTimeout time.Duration `yaml:"-"`
	ShutdownRaw     string        `yaml:"shutdown_timeout"`
}

type DatabaseConfig struct {
	URL            string        `yaml:"url"`
	ConnectTimeout time.Duration `yaml:"-"`
	ConnectRaw     string        `yaml:"connect_timeout"`
	MaxConnections int32         `yaml:"max_connections"`
	MinConnections int32         `yaml:"min_connections"`
}

type WorkerConfig struct {
	HeartbeatInterval time.Duration `yaml:"-"`
	HeartbeatRaw      string        `yaml:"heartbeat_interval"`
}

// Default 返回适合本地开发的非敏感默认配置。
func Default() Config {
	return Config{
		App: AppConfig{
			Name:        "velis",
			Environment: "development",
			LogLevel:    "info",
		},
		HTTP: HTTPConfig{
			Address:     "0.0.0.0:8080",
			ShutdownRaw: "10s",
		},
		Database: DatabaseConfig{
			URL:            "postgres://velis:velis@localhost:5432/velis?sslmode=disable",
			ConnectRaw:     "5s",
			MaxConnections: 20,
			MinConnections: 2,
		},
		Worker: WorkerConfig{HeartbeatRaw: "30s"},
		Assets: AssetConfig{
			Bucket:            "velis-article-assets",
			UploadRaw:         "15m",
			PendingRaw:        "24h",
			UnboundRaw:        "168h",
			MaxFileBytes:      10 * 1024 * 1024,
			MaxWidth:          8192,
			MaxHeight:         8192,
			MaxPixels:         40_000_000,
			UserQuotaBytes:    1024 * 1024 * 1024,
			PendingLimit:      20,
			ArticleImageLimit: 20,
			ArticleBytesLimit: 50 * 1024 * 1024,
		},
		Idempotency: IdempotencyConfig{RetentionRaw: "24h"},
		Auth: AuthConfig{
			Enabled:             false,
			RegistrationEnabled: false,
			AccessRaw:           "15m",
			SessionRaw:          "168h",
			JWTIssuer:           "velis-api",
			JWTAudience:         "velis-web",
			CookieSecure:        true,
			CleanupRaw:          "1h",
			CleanupBatch:        500,
			Throttle: ThrottleConfig{
				AccountWindowRaw: "15m",
				AccountLimit:     5,
				IPWindowRaw:      "15m",
				IPLimit:          30,
				BlockRaw:         "15m",
			},
			Argon2: Argon2Config{
				MemoryKiB:   19456,
				Iterations:  2,
				Parallelism: 1,
				SaltBytes:   16,
				KeyBytes:    32,
				Concurrency: 1,
			},
		},
	}
}

// Load 先读取 YAML，再使用 VELIS_* 环境变量覆盖，最后统一校验。
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("读取配置文件 %q: %w", path, err)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("解析配置文件 %q: %w", path, err)
		}
	}

	if err := applyEnvironment(&cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("配置校验失败: %w", err)
	}
	return cfg, nil
}

func applyEnvironment(cfg *Config) error {
	setString(&cfg.App.Name, "VELIS_APP_NAME")
	setString(&cfg.App.Environment, "VELIS_APP_ENVIRONMENT")
	setString(&cfg.App.LogLevel, "VELIS_LOG_LEVEL")
	setString(&cfg.HTTP.Address, "VELIS_HTTP_ADDRESS")
	setString(&cfg.HTTP.ShutdownRaw, "VELIS_HTTP_SHUTDOWN_TIMEOUT")
	setString(&cfg.Database.URL, "VELIS_DATABASE_URL")
	setString(&cfg.Database.ConnectRaw, "VELIS_DATABASE_CONNECT_TIMEOUT")
	setString(&cfg.Worker.HeartbeatRaw, "VELIS_WORKER_HEARTBEAT_INTERVAL")
	setString(&cfg.Assets.Endpoint, "VELIS_ASSET_ENDPOINT")
	setString(&cfg.Assets.Bucket, "VELIS_ASSET_BUCKET")
	setString(&cfg.Assets.AccessKey, "VELIS_ASSET_ACCESS_KEY")
	setString(&cfg.Assets.SecretKey, "VELIS_ASSET_SECRET_KEY")
	setString(&cfg.Assets.WebOrigin, "VELIS_ASSET_WEB_ORIGIN")
	setString(&cfg.Assets.UploadRaw, "VELIS_ASSET_UPLOAD_TTL")
	setString(&cfg.Assets.PendingRaw, "VELIS_ASSET_PENDING_TTL")
	setString(&cfg.Assets.UnboundRaw, "VELIS_ASSET_UNBOUND_TTL")
	setString(&cfg.Idempotency.RetentionRaw, "VELIS_IDEMPOTENCY_RETENTION")
	setString(&cfg.Feed.ProxyURL, "VELIS_FEED_PROXY_URL")

	if err := setInt32(&cfg.Database.MaxConnections, "VELIS_DATABASE_MAX_CONNECTIONS"); err != nil {
		return err
	}
	if err := setInt32(&cfg.Database.MinConnections, "VELIS_DATABASE_MIN_CONNECTIONS"); err != nil {
		return err
	}
	if err := setBool(&cfg.Assets.UseTLS, "VELIS_ASSET_USE_TLS"); err != nil {
		return err
	}
	for target, key := range map[*int64]string{
		&cfg.Assets.MaxFileBytes:      "VELIS_ASSET_MAX_FILE_BYTES",
		&cfg.Assets.MaxPixels:         "VELIS_ASSET_MAX_PIXELS",
		&cfg.Assets.UserQuotaBytes:    "VELIS_ASSET_USER_QUOTA_BYTES",
		&cfg.Assets.ArticleBytesLimit: "VELIS_ASSET_ARTICLE_BYTES_LIMIT",
	} {
		if err := setInt64(target, key); err != nil {
			return err
		}
	}
	for target, key := range map[*int]string{
		&cfg.Assets.MaxWidth:          "VELIS_ASSET_MAX_WIDTH",
		&cfg.Assets.MaxHeight:         "VELIS_ASSET_MAX_HEIGHT",
		&cfg.Assets.PendingLimit:      "VELIS_ASSET_PENDING_LIMIT",
		&cfg.Assets.ArticleImageLimit: "VELIS_ASSET_ARTICLE_IMAGE_LIMIT",
	} {
		if err := setInt(target, key); err != nil {
			return err
		}
	}
	return applyAuthEnvironment(cfg)
}

func applyAuthEnvironment(cfg *Config) error {
	if err := setBool(&cfg.Auth.Enabled, "VELIS_AUTH_ENABLED"); err != nil {
		return err
	}
	if err := setBool(&cfg.Auth.RegistrationEnabled, "VELIS_AUTH_REGISTRATION_ENABLED"); err != nil {
		return err
	}
	setString(&cfg.Auth.AccessRaw, "VELIS_AUTH_ACCESS_TTL")
	setString(&cfg.Auth.SessionRaw, "VELIS_AUTH_SESSION_TTL")
	setString(&cfg.Auth.JWTIssuer, "VELIS_AUTH_JWT_ISSUER")
	setString(&cfg.Auth.JWTAudience, "VELIS_AUTH_JWT_AUDIENCE")
	setString(&cfg.Auth.JWTActiveKID, "VELIS_AUTH_JWT_ACTIVE_KID")
	setString(&cfg.Auth.JWTActiveKey, "VELIS_AUTH_JWT_ACTIVE_KEY")
	setString(&cfg.Auth.JWTPreviousKID, "VELIS_AUTH_JWT_PREVIOUS_KID")
	setString(&cfg.Auth.JWTPreviousKey, "VELIS_AUTH_JWT_PREVIOUS_KEY")
	setString(&cfg.Auth.AllowedOrigin, "VELIS_AUTH_ALLOWED_ORIGIN")
	setString(&cfg.Auth.ThrottleKey, "VELIS_AUTH_THROTTLE_KEY")
	setString(&cfg.Auth.CleanupRaw, "VELIS_AUTH_CLEANUP_INTERVAL")
	setString(&cfg.Auth.Throttle.AccountWindowRaw, "VELIS_AUTH_THROTTLE_ACCOUNT_WINDOW")
	setString(&cfg.Auth.Throttle.IPWindowRaw, "VELIS_AUTH_THROTTLE_IP_WINDOW")
	setString(&cfg.Auth.Throttle.BlockRaw, "VELIS_AUTH_THROTTLE_BLOCK_DURATION")

	if err := setBool(&cfg.Auth.CookieSecure, "VELIS_AUTH_COOKIE_SECURE"); err != nil {
		return err
	}
	if err := setInt(&cfg.Auth.CleanupBatch, "VELIS_AUTH_CLEANUP_BATCH"); err != nil {
		return err
	}
	if err := setInt(&cfg.Auth.Throttle.AccountLimit, "VELIS_AUTH_THROTTLE_ACCOUNT_LIMIT"); err != nil {
		return err
	}
	if err := setInt(&cfg.Auth.Throttle.IPLimit, "VELIS_AUTH_THROTTLE_IP_LIMIT"); err != nil {
		return err
	}
	if err := setUint32(&cfg.Auth.Argon2.MemoryKiB, "VELIS_AUTH_ARGON2_MEMORY_KIB"); err != nil {
		return err
	}
	if err := setUint32(&cfg.Auth.Argon2.Iterations, "VELIS_AUTH_ARGON2_ITERATIONS"); err != nil {
		return err
	}
	if err := setInt(&cfg.Auth.Argon2.SaltBytes, "VELIS_AUTH_ARGON2_SALT_BYTES"); err != nil {
		return err
	}
	if err := setInt(&cfg.Auth.Argon2.KeyBytes, "VELIS_AUTH_ARGON2_KEY_BYTES"); err != nil {
		return err
	}
	if err := setInt(&cfg.Auth.Argon2.Concurrency, "VELIS_AUTH_ARGON2_CONCURRENCY"); err != nil {
		return err
	}
	if err := setUint8(&cfg.Auth.Argon2.Parallelism, "VELIS_AUTH_ARGON2_PARALLELISM"); err != nil {
		return err
	}
	if value, ok := os.LookupEnv("VELIS_AUTH_TRUSTED_PROXY_CIDRS"); ok {
		cfg.Auth.TrustedProxyCIDRs = splitList(value)
	}
	return nil
}

func splitList(value string) []string {
	items := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

func setString(target *string, key string) {
	if value, ok := os.LookupEnv(key); ok {
		*target = value
	}
}

func setBool(target *bool, key string) error {
	value, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fmt.Errorf("环境变量 %s 必须是布尔值: %w", key, err)
	}
	*target = parsed
	return nil
}

func setInt(target *int, key string) error {
	value, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("环境变量 %s 必须是整数: %w", key, err)
	}
	*target = parsed
	return nil
}

func setUint32(target *uint32, key string) error {
	value, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return fmt.Errorf("环境变量 %s 必须是非负整数: %w", key, err)
	}
	*target = uint32(parsed)
	return nil
}

func setUint8(target *uint8, key string) error {
	value, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	parsed, err := strconv.ParseUint(value, 10, 8)
	if err != nil {
		return fmt.Errorf("环境变量 %s 必须是非负整数: %w", key, err)
	}
	*target = uint8(parsed)
	return nil
}

func setInt32(target *int32, key string) error {
	value, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return fmt.Errorf("环境变量 %s 必须是整数: %w", key, err)
	}
	*target = int32(parsed)
	return nil
}

func setInt64(target *int64, key string) error {
	value, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fmt.Errorf("环境变量 %s 必须是整数: %w", key, err)
	}
	*target = parsed
	return nil
}

func (cfg *Config) Validate() error {
	if strings.TrimSpace(cfg.App.Name) == "" {
		return errors.New("app.name 不能为空")
	}
	if _, _, err := net.SplitHostPort(cfg.HTTP.Address); err != nil {
		return fmt.Errorf("http.address 必须是 host:port: %w", err)
	}

	var err error
	if cfg.HTTP.ShutdownTimeout, err = positiveDuration("http.shutdown_timeout", cfg.HTTP.ShutdownRaw); err != nil {
		return err
	}
	if cfg.Database.ConnectTimeout, err = positiveDuration("database.connect_timeout", cfg.Database.ConnectRaw); err != nil {
		return err
	}
	if cfg.Worker.HeartbeatInterval, err = positiveDuration("worker.heartbeat_interval", cfg.Worker.HeartbeatRaw); err != nil {
		return err
	}
	if _, err := pgxpool.ParseConfig(cfg.Database.URL); err != nil {
		return fmt.Errorf("database.url 无效: %w", err)
	}
	if cfg.Database.MaxConnections <= 0 {
		return errors.New("database.max_connections 必须大于 0")
	}
	if cfg.Database.MinConnections < 0 || cfg.Database.MinConnections > cfg.Database.MaxConnections {
		return errors.New("database.min_connections 必须介于 0 和 max_connections 之间")
	}
	if err := validateContentConfig(cfg); err != nil {
		return err
	}
	return validateAuth(&cfg.Auth, cfg.App.Environment)
}

func positiveDuration(name, raw string) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s 无效: %w", name, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s 必须大于 0", name)
	}
	return d, nil
}
