package bootstrap

import (
	"context"
	"testing"
	"time"

	cacheApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/prometheus/client_golang/prometheus"
)

type cacheTestPinger struct{}

func (cacheTestPinger) Ping(context.Context) error { return nil }
func TestReadCacheDisabledUnreachableAndReadiness(t *testing.T) {
	cfg := config.Default()
	cfg.Cache.Redis.Address = "bad-address"
	r, err := buildReadCache(cfg, prometheus.NewRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Cache.(cacheApp.Disabled); !ok {
		t.Fatal("关闭路径未使用 Disabled")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	cfg.Cache.Enabled = true
	if _, err := buildReadCache(cfg, prometheus.NewRegistry(), nil); err == nil {
		t.Fatal("启用静态错误未拒绝")
	}
	cfg.Cache.Redis.Address = "127.0.0.1:1"
	start := time.Now()
	r, err = buildReadCache(cfg, prometheus.NewRegistry(), nil)
	if err != nil {
		t.Fatal("运行时不可达阻止装配", err)
	}
	defer r.Close()
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("装配尝试网络探测")
	}
	ctx := r.Cache.NewRequest(context.Background())
	v, err := r.Cache.GetCards(ctx, []cacheApp.CardIdentity{{ArticleID: 1, RevisionID: 1}})
	if err != nil || len(v) != 0 {
		t.Fatal("不可达未安全绕过", v, err)
	}
	service := health.NewService(cacheTestPinger{})
	if err := service.Ready(context.Background()); err != nil {
		t.Fatal("Redis 影响 readiness", err)
	}
}
