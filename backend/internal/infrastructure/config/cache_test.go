package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCacheDefaultsPrecedenceAndSecrets(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Cache.Enabled {
		t.Fatal("默认开启缓存")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("app:\n  environment: development\ncache:\n  enabled: true\n  redis:\n    address: localhost:6379\n    username: ignored\n    password: ignored\n  latest_ttl: 2s\n  pool_size: 4\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VELIS_CACHE_LATEST_TTL", "3s")
	t.Setenv("VELIS_CACHE_REDIS_PASSWORD", "secret-value")
	t.Setenv("VELIS_CACHE_POOL_SIZE", "7")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Cache.LatestTTL != 3*time.Second || cfg.Cache.PoolSize != 7 || cfg.Cache.Namespace != "velis:development" || cfg.Cache.OperationTimeout != 50*time.Millisecond || cfg.Cache.RequestBudget != 100*time.Millisecond {
		t.Fatalf("优先级或默认值错误: %+v", cfg.Cache)
	}
	if string(cfg.Cache.Redis.Password) != "secret-value" || len(cfg.Cache.Redis.Username) != 0 {
		t.Fatal("凭据未仅从环境注入")
	}
	if strings.Contains(fmt.Sprintf("%+v", cfg.Cache), "secret-value") {
		t.Fatal("配置格式化泄露凭据")
	}
	t.Setenv("VELIS_CACHE_ENABLED", "false")
	t.Setenv("VELIS_CACHE_LATEST_TTL", "invalid")
	cfg, err = Load(path)
	if err != nil || cfg.Cache.Enabled {
		t.Fatal(cfg, err)
	}
}
func TestCacheStaticValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*CacheConfig)
	}{
		{"address", func(c *CacheConfig) { c.Redis.Address = "" }},
		{"url", func(c *CacheConfig) { c.Redis.Address = "redis://name:password@host:6379" }},
		{"embedded_username", func(c *CacheConfig) { c.Redis.Address = "name@host:6379" }},
		{"port", func(c *CacheConfig) { c.Redis.Address = "localhost:0" }},
		{"namespace", func(c *CacheConfig) { c.Namespace = "bad*" }},
		{"long_namespace", func(c *CacheConfig) { c.Namespace = strings.Repeat("n", 97) }},
		{"db", func(c *CacheConfig) { c.Redis.Database = -1 }},
		{"pool", func(c *CacheConfig) { c.PoolSize = 101 }},
		{"batch", func(c *CacheConfig) { c.BatchSize = 0 }},
		{"card_ttl", func(c *CacheConfig) { c.CardTTLRaw = "2h" }},
		{"latest_ttl", func(c *CacheConfig) { c.LatestTTLRaw = "6s" }},
		{"plan_ttl", func(c *CacheConfig) { c.RecommendTTLRaw = "31s" }},
		{"operation", func(c *CacheConfig) { c.OperationRaw = "4ms" }},
		{"budget", func(c *CacheConfig) { c.BudgetRaw = "201ms" }},
		{"operation_over_budget", func(c *CacheConfig) { c.OperationRaw = "100ms"; c.BudgetRaw = "10ms" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			cfg.Cache.Enabled = true
			cfg.Cache.Redis.Address = "127.0.0.1:6379"
			tc.edit(&cfg.Cache)
			if err := cfg.Validate(); err == nil {
				t.Fatal("接受了非法配置")
			}
		})
	}
	for _, key := range []string{"VELIS_CACHE_ENABLED", "VELIS_CACHE_REDIS_TLS", "VELIS_CACHE_REDIS_DATABASE", "VELIS_CACHE_POOL_SIZE", "VELIS_CACHE_BATCH_SIZE"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "bad")
			if _, err := Load(""); err == nil {
				t.Fatal("非法环境变量未拒绝")
			}
		})
	}
}
