package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecommendDefaultsAndDevelopmentKey(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	r := cfg.Recommend
	if r.FirstQueryTimeout != 5*time.Second || r.CursorTTL != 2*time.Minute || r.BM25Candidates != 100 || r.KNNCandidates != 100 || !r.CursorKeyEphemeral || len(r.CursorKey.Bytes()) != 32 {
		t.Fatalf("推荐默认配置错误: %+v", r)
	}
	if strings.Contains(fmt.Sprintf("%+v", r), base64.StdEncoding.EncodeToString(r.CursorKey.Bytes())) {
		t.Fatal("格式化泄露游标密钥")
	}
}

func TestRecommendProductionSharedKeyEnvironmentOnly(t *testing.T) {
	key := []byte("recommend-cursor-key-01234567890123")
	t.Setenv("VELIS_APP_ENVIRONMENT", "production")
	t.Setenv("VELIS_RECOMMEND_CURSOR_KEY", base64.StdEncoding.EncodeToString(key))
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("recommend:\n  cursor_key: ignored\n  first_query_timeout: 4s\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(cfg.Recommend.CursorKey.Bytes()); got != string(key) || cfg.Recommend.CursorKeyEphemeral || cfg.Recommend.FirstQueryTimeout != 4*time.Second {
		t.Fatalf("生产推荐密钥或 YAML 配置错误: %+v", cfg.Recommend)
	}
	if strings.Contains(fmt.Sprintf("%+v", cfg.Recommend), string(key)) {
		t.Fatal("格式化泄露密钥")
	}
}

func TestRecommendRejectsInvalidBounds(t *testing.T) {
	t.Setenv("VELIS_RECOMMEND_CURSOR_KEY", "bad")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "VELIS_RECOMMEND_CURSOR_KEY") {
		t.Fatalf("非法密钥未拒绝: %v", err)
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Recommend.FirstQueryRaw = "0s" },
		func(c *Config) { c.Recommend.FirstQueryRaw = "31s" },
		func(c *Config) { c.Recommend.CursorTTLRaw = "20s" },
		func(c *Config) { c.Recommend.BM25Candidates = 0 },
		func(c *Config) { c.Recommend.KNNCandidates = 101 },
	} {
		cfg := Default()
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Fatal("非法推荐配置未拒绝")
		}
	}
	production := Default()
	production.App.Environment = "production"
	if err := production.Validate(); err != nil || production.Recommend.CursorKey.IsSet() {
		t.Fatalf("非 API 进程不应因推荐密钥缺失而无法加载生产配置: %v", err)
	}
}
