// Package bootstrap 是进程的 Composition Root，可以装配所有层。
package bootstrap

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	sourceApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/clock"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/fetcher/httpfeed"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	hertzhttp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz"
)

func RunAPI(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	content := buildContentServices(cfg, pool, logger)
	healthService := health.NewService(pool)
	feed := buildFeedServices(pool, cfg.Feed.ProxyURL)
	options := hertzhttp.Options{
		Address:         cfg.HTTP.Address,
		ShutdownTimeout: cfg.HTTP.ShutdownTimeout,
		Logger:          logger,
		Health:          healthService,
		Articles:        feed.articles,
		Assets:          content.assets,
	}
	if cfg.Auth.Enabled {
		accountService, err := buildAuthService(cfg, pool, logger)
		if err != nil {
			return err
		}
		proxies, err := parseTrustedProxies(cfg.Auth.TrustedProxyCIDRs)
		if err != nil {
			return err
		}
		options.Auth = &hertzhttp.AuthOptions{
			AdminSources: sourceApp.NewAdminService(postgres.NewSourceRepository(pool),
				postgres.NewFetchRunRepository(pool), feed.sources, content.idempotency, clock.System{}),
			Service:        accountService,
			AllowedOrigin:  cfg.Auth.AllowedOrigin,
			TrustedProxies: proxies,
			CookieSecure:   cfg.Auth.CookieSecure,
			Clock:          clock.System{},
			MyArticles:     content.myArticles,
			AdminArticles:  content.adminArticles,
			Assets:         content.assets,
		}
	}
	// 启动自检只报告存储状态，不阻止启动：资产能力按请求降级，readyz 仍以 PostgreSQL 为准。
	if content.store != nil {
		if err := content.store.EnsurePrivateBucket(ctx); err != nil {
			logger.Warn("资产存储未就绪，图片能力降级", "error", err)
		} else {
			logger.Info("资产存储已就绪", "bucket", cfg.Assets.Bucket)
		}
	}
	h := hertzhttp.NewServer(options)
	errCh := make(chan error, 1)
	go func() {
		errCh <- h.Run()
	}()

	feedMode, feedProxyHost := httpfeed.NetworkMode(cfg.Feed.ProxyURL)
	logger.Info("velis-api 已启动",
		"feed_network_mode", feedMode,
		"feed_proxy_host", feedProxyHost,
		"address", cfg.HTTP.Address,
		"environment", cfg.App.Environment,
		"database_max_connections", cfg.Database.MaxConnections,
		"auth_enabled", cfg.Auth.Enabled,
		"registration_enabled", cfg.Auth.RegistrationEnabled,
		// 记录实际生效的网段而不是数量：配错可信代理会让 IP 维度限流退化为全局共享，
		// 只有把取值本身打出来，运维才能从启动日志发现「配了但配错」。
		"trusted_proxy_cidrs", cfg.Auth.TrustedProxyCIDRs,
	)

	select {
	case err := <-errCh:
		if err != nil {
			return errors.New("Hertz 服务异常退出: " + err.Error())
		}
		return nil
	case <-ctx.Done():
		healthService.SetDraining(true)
		logger.Info("velis-api 开始优雅关闭")
		if err := h.Shutdown(context.Background()); err != nil {
			return errors.New("Hertz 服务关闭失败: " + err.Error())
		}
		if err := <-errCh; err != nil {
			return errors.New("Hertz 服务退出失败: " + err.Error())
		}
		return nil
	}
}
