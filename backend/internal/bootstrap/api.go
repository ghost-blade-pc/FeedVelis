// Package bootstrap 是进程的 Composition Root，可以装配所有层。
package bootstrap

import (
	"context"
	"errors"
	"io"
	"log/slog"

	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	sourceApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/clock"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/fetcher/httpfeed"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/observability"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	searchAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/search/opensearch"
	hertzhttp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
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
	registry := prometheus.NewRegistry()
	queryMetrics := observability.NewArticleSearchMetrics(registry)
	search, searchClient := buildArticleSearch(cfg, pool, observability.NewArticleSearchObserver(queryMetrics), logger)
	if searchClient != nil {
		defer searchClient.Close()
	}
	options := hertzhttp.Options{
		Address:         cfg.HTTP.Address,
		ShutdownTimeout: cfg.HTTP.ShutdownTimeout,
		Logger:          logger,
		Health:          healthService,
		Articles:        feed.articles,
		Search:          search,
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
	metricsContext, stopMetrics := context.WithCancel(context.Background())
	defer stopMetrics()
	metrics := observability.NewMetricsServer(cfg.HTTP.MetricsAddress, registry)
	go func() {
		if metricsErr := metrics.Run(metricsContext); metricsErr != nil {
			logger.Warn("API metrics listener 不可用，不影响核心 readiness", "error", metricsErr)
		}
	}()
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

func buildArticleSearch(cfg config.Config, pool *pgxpool.Pool, observer searchApp.Observer, logger *slog.Logger) (searchApp.Searcher, io.Closer) {
	if !cfg.Search.QueryEnabled() {
		if cfg.Search.Enabled() && logger != nil {
			logger.Warn("搜索查询未装配：缺少生产 cursor key")
		}
		return searchApp.UnavailableService{}, nil
	}
	client, err := searchAdapter.New(searchClientConfig(cfg))
	if err != nil {
		if logger != nil {
			logger.Warn("搜索查询客户端装配失败，路由降级为不可用", "error", err)
		}
		return searchApp.UnavailableService{}, nil
	}
	codec, err := searchApp.NewCursorCodec(cfg.Search.Query.CursorKey.Bytes())
	if err != nil {
		_ = client.Close()
		return searchApp.UnavailableService{}, nil
	}
	service := searchApp.NewService(client, postgres.NewArticleRepository(pool), codec, observer, searchApp.Config{
		PITKeepAlive: cfg.Search.Query.PITKeepAlive, CandidateBatchSize: cfg.Search.Query.CandidateBatchSize,
		MaxCandidatesPerRequest: cfg.Search.Query.MaxCandidatesPerRequest,
	})
	return service, client
}
