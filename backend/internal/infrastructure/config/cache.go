package config

import (
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type CacheConfig struct {
	Enabled          bool             `yaml:"enabled"`
	Redis            CacheRedisConfig `yaml:"redis"`
	Namespace        string           `yaml:"namespace"`
	CardTTLRaw       string           `yaml:"card_ttl"`
	LatestTTLRaw     string           `yaml:"latest_ttl"`
	RecommendTTLRaw  string           `yaml:"recommend_ttl"`
	OperationRaw     string           `yaml:"operation_timeout"`
	BudgetRaw        string           `yaml:"request_budget"`
	CardTTL          time.Duration    `yaml:"-"`
	LatestTTL        time.Duration    `yaml:"-"`
	RecommendTTL     time.Duration    `yaml:"-"`
	OperationTimeout time.Duration    `yaml:"-"`
	RequestBudget    time.Duration    `yaml:"-"`
	PoolSize         int              `yaml:"pool_size"`
	BatchSize        int              `yaml:"batch_size"`
}
type CacheRedisConfig struct {
	Address  string      `yaml:"address"`
	Database int         `yaml:"database"`
	TLS      bool        `yaml:"tls"`
	Username SecretBytes `yaml:"-"`
	Password SecretBytes `yaml:"-"`
}

func defaultCacheConfig() CacheConfig {
	return CacheConfig{CardTTLRaw: "5m", LatestTTLRaw: "5s", RecommendTTLRaw: "30s", OperationRaw: "50ms", BudgetRaw: "100ms", PoolSize: 10, BatchSize: 100}
}
func applyCacheEnvironment(cfg *Config) error {
	c := &cfg.Cache
	for target, key := range map[*string]string{&c.Namespace: "VELIS_CACHE_NAMESPACE", &c.Redis.Address: "VELIS_CACHE_REDIS_ADDRESS", &c.CardTTLRaw: "VELIS_CACHE_CARD_TTL", &c.LatestTTLRaw: "VELIS_CACHE_LATEST_TTL", &c.RecommendTTLRaw: "VELIS_CACHE_RECOMMEND_TTL", &c.OperationRaw: "VELIS_CACHE_OPERATION_TIMEOUT", &c.BudgetRaw: "VELIS_CACHE_REQUEST_BUDGET"} {
		setString(target, key)
	}
	var username, password string
	setString(&username, "VELIS_CACHE_REDIS_USERNAME")
	setString(&password, "VELIS_CACHE_REDIS_PASSWORD")
	c.Redis.Username = SecretBytes(username)
	c.Redis.Password = SecretBytes(password)
	for target, key := range map[*int]string{&c.Redis.Database: "VELIS_CACHE_REDIS_DATABASE", &c.PoolSize: "VELIS_CACHE_POOL_SIZE", &c.BatchSize: "VELIS_CACHE_BATCH_SIZE"} {
		if err := setInt(target, key); err != nil {
			return err
		}
	}
	if err := setBool(&c.Enabled, "VELIS_CACHE_ENABLED"); err != nil {
		return err
	}
	return setBool(&c.Redis.TLS, "VELIS_CACHE_REDIS_TLS")
}

var cacheNamespacePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9:_-]{0,95}$`)

// ValidateCache 也供适配器构造使用，不探测运行时可达性。
func ValidateCache(c *CacheConfig, environment string) error {
	if !c.Enabled {
		return nil
	}
	if c.Namespace == "" {
		c.Namespace = "velis:" + environment
	}
	if !cacheNamespacePattern.MatchString(c.Namespace) {
		return errors.New("cache.namespace 必须为 1..96 位字母、数字、冒号、下划线或连字符")
	}
	host, port, err := net.SplitHostPort(c.Redis.Address)
	n, portErr := strconv.Atoi(port)
	if err != nil || host == "" || strings.ContainsAny(host, "@/\\?# \t\r\n") || portErr != nil || n < 1 || n > 65535 {
		return errors.New("cache.redis.address 必须为有效 host:port")
	}
	if c.Redis.Database < 0 {
		return errors.New("cache.redis.database 必须非负")
	}
	if c.PoolSize < 1 || c.PoolSize > 100 || c.BatchSize < 1 || c.BatchSize > 100 {
		return errors.New("cache pool_size/batch_size 必须介于 1 和 100")
	}
	for _, d := range []struct {
		name, raw string
		target    *time.Duration
		low, high time.Duration
	}{
		{"card_ttl", c.CardTTLRaw, &c.CardTTL, time.Second, time.Hour},
		{"latest_ttl", c.LatestTTLRaw, &c.LatestTTL, time.Second, 5 * time.Second},
		{"recommend_ttl", c.RecommendTTLRaw, &c.RecommendTTL, time.Second, 30 * time.Second},
		{"operation_timeout", c.OperationRaw, &c.OperationTimeout, 5 * time.Millisecond, 100 * time.Millisecond},
		{"request_budget", c.BudgetRaw, &c.RequestBudget, 10 * time.Millisecond, 200 * time.Millisecond},
	} {
		value, err := durationWithin("cache."+d.name, d.raw, d.low, d.high)
		if err != nil {
			return err
		}
		*d.target = value
	}
	if c.OperationTimeout > c.RequestBudget {
		return errors.New("cache.operation_timeout 不能大于 request_budget")
	}
	return nil
}
