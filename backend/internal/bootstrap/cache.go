package bootstrap

import (
	"io"
	"log/slog"

	cacheApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	redisAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/cache/redis"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/observability"
	"github.com/prometheus/client_golang/prometheus"
)

// readCacheResources 是读取编排与提交后失效共用的进程资源，生命周期由入口持有。
type readCacheResources struct {
	Cache    cacheApp.Cache
	Observer cacheApp.Observer
	closer   io.Closer
}

func (r *readCacheResources) Close() error {
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}
func buildReadCache(cfg config.Config, registry prometheus.Registerer, logger *slog.Logger) (*readCacheResources, error) {
	observer := observability.NewCacheMetrics(registry, logger)
	if !cfg.Cache.Enabled {
		return &readCacheResources{Cache: cacheApp.Disabled{}, Observer: observer}, nil
	}
	cacheCfg := cfg.Cache
	if err := config.ValidateCache(&cacheCfg, cfg.App.Environment); err != nil {
		return nil, err
	}
	adapter, err := redisAdapter.New(cacheCfg, observer, nil)
	if err != nil {
		return nil, err
	}
	return &readCacheResources{Cache: adapter, Observer: observer, closer: adapter}, nil
}
