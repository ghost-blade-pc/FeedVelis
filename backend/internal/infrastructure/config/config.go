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
	App      AppConfig      `yaml:"app"`
	HTTP     HTTPConfig     `yaml:"http"`
	Database DatabaseConfig `yaml:"database"`
	Worker   WorkerConfig   `yaml:"worker"`
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

	if err := setInt32(&cfg.Database.MaxConnections, "VELIS_DATABASE_MAX_CONNECTIONS"); err != nil {
		return err
	}
	return setInt32(&cfg.Database.MinConnections, "VELIS_DATABASE_MIN_CONNECTIONS")
}

func setString(target *string, key string) {
	if value, ok := os.LookupEnv(key); ok {
		*target = value
	}
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
	return nil
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
