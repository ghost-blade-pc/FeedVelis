// Package bootstrap 是进程的 Composition Root，可以装配所有层。
package bootstrap

import (
	"context"
	"errors"
	"io"
	"log/slog"

	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentconversation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	feedbackApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlefeedback"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleread"
	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	sourceApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/source"
	einoAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/ai/eino"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/clock"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/fetcher/httpfeed"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/observability"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	searchAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/search/opensearch"
	hertzhttp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/handler"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

func RunAPI(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	agentKey, ephemeral, err := cfg.AgentAPIKey()
	if err != nil {
		return err
	}
	if ephemeral && logger != nil {
		logger.Warn("Agent 使用临时游标密钥，重启后旧游标失效")
	}
	pool, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	healthService := health.NewService(pool)
	registry := prometheus.NewRegistry()
	readCache, err := buildReadCache(cfg, registry, logger)
	if err != nil {
		return err
	}
	defer readCache.Close()
	var shared *articleread.Service
	content := buildContentServices(cfg, pool, logger)
	feed := buildFeedServices(pool, cfg.Feed.ProxyURL)
	if cfg.Cache.Enabled {
		shared = articleread.NewService(postgres.NewArticleRepository(pool), postgres.NewTxManager(pool), readCache.Cache, readCache.Observer, cfg.Cache.BatchSize, nil)
		invalidator := articleread.NewInvalidator(postgres.NewTxManager(pool), readCache.Cache, ctx, cfg.Cache.RequestBudget)
		content.myArticles.WithPublicReadInvalidator(invalidator)
		content.adminArticles.WithPublicReadInvalidator(invalidator)
		feed.articles.WithLatestReader(shared).WithPublicReadInvalidator(invalidator)
	}

	queryMetrics := observability.NewArticleSearchMetrics(registry)
	search, searchClient := buildArticleSearch(cfg, pool, observability.NewArticleSearchObserver(queryMetrics).WithLogger(logger), logger, shared)
	if searchClient != nil {
		defer searchClient.Close()
	}
	recommend, recommendCloser, err := buildRecommendation(cfg, pool, logger, shared)
	if err != nil {
		return err
	}
	if recommendCloser != nil {
		defer recommendCloser.Close()
	}
	if cfg.Cache.Enabled {
		recommend.WithPlanCache(readCache.Cache, func(ctx context.Context) { readCache.Observer.Fallback(ctx, articlecache.Recommend, 1) })
	}
	recommend.WithObserver(observability.NewRecommendObserver(observability.NewRecommendMetrics(registry), logger))
	options := hertzhttp.Options{
		Address:         cfg.HTTP.Address,
		ShutdownTimeout: cfg.HTTP.ShutdownTimeout,
		Logger:          logger,
		Health:          healthService,
		Articles:        feed.articles,
		Search:          search,
		Recommend:       recommend,
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
			Feedback:       feedbackApp.NewService(postgres.NewArticleFeedbackRepository(pool), clock.System{}),
		}
		if cfg.Agent.Enabled {
			agentObserver := observability.NewAgentObserver(registry, logger)
			codec, err := agent.NewCursorCodec(agentKey)
			if err != nil {
				return err
			}
			tx := postgres.NewTxManager(pool)
			service := agent.NewService(postgres.NewAgentConversationRepository(pool), tx, tx, postgres.NewIdempotencyRepository(pool), agent.Limits{
				MaxConversations: cfg.Agent.MaxConversationsPerUser, MaxMessages: cfg.Agent.MaxMessagesPerConversation, MaxMessageChars: cfg.Agent.MaxMessageChars,
			})
			options.Auth.Agent = handler.NewAgentConversation(service, codec, cfg.Agent.CursorTTL, cfg.Agent.MaxMessageChars).WithObserver(agentObserver)
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
		"recommend_cursor_key_ephemeral", cfg.Recommend.CursorKeyEphemeral,
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

func buildRecommendation(cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger, sharedReaders ...*articleread.Service) (*recommendation.Service, io.Closer, error) {
	if cfg.App.Environment != "development" && !cfg.Recommend.CursorKey.IsSet() {
		return nil, nil, errors.New("生产 API 必须配置 VELIS_RECOMMEND_CURSOR_KEY")
	}
	codec, err := recommendation.NewCursorCodec(cfg.Recommend.CursorKey.Bytes())
	if err != nil {
		return nil, nil, err
	}
	var index recommendation.CandidateIndex
	var client io.Closer
	if cfg.Search.Enabled() {
		adapter, openErr := searchAdapter.New(searchClientConfig(cfg))
		if openErr == nil {
			index, client = adapter, adapter
		} else if logger != nil {
			logger.Warn("推荐候选客户端装配失败，按 latest 降级")
		}
	}
	var embed recommendation.QueryEmbedder
	var modelCloser io.Closer
	if cfg.Search.Query.Hybrid.Enabled && cfg.AI.Embedding.Profile.Enabled() {
		adapter, openErr := einoAdapter.NewOpenAIQueryEmbedder(context.Background(), cfg.AI.Embedding, cfg.Search.Query.Hybrid.EmbeddingTimeout)
		if openErr == nil {
			embed, modelCloser = adapter, adapter
		} else if logger != nil {
			logger.Warn("推荐查询 Embedding 装配失败，保留词项候选")
		}
	}
	var reader recommendation.Reader = postgres.NewArticleRepository(pool)
	if len(sharedReaders) > 0 && sharedReaders[0] != nil {
		reader = sharedReaders[0]
	}
	profile := feedbackApp.NewProfileService(postgres.NewArticleFeedbackRepository(pool), clock.System{})
	service := recommendation.NewService(profile, index, reader, codec, embed, recommendation.Config{
		FirstQueryTimeout: cfg.Recommend.FirstQueryTimeout, BM25Candidates: cfg.Recommend.BM25Candidates, KNNCandidates: cfg.Recommend.KNNCandidates,
		CursorTTL: cfg.Recommend.CursorTTL, EmbeddingTimeout: cfg.Search.Query.Hybrid.EmbeddingTimeout, KNNTimeout: cfg.Search.Query.Hybrid.KNNTimeout,
		EmbeddingProfile: cfg.AI.Embedding.Profile.ProfileVersion, Dimensions: cfg.AI.Embedding.Dimensions,
		SemanticEnabled: cfg.Search.Query.Hybrid.Enabled,
		Provider:        cfg.AI.Embedding.Profile.Provider, Model: cfg.AI.Embedding.Profile.Model, EmbeddingInputVersion: cfg.AI.Embedding.InputVersion,
	}, nil)
	return service, &recommendCloser{index: client, model: modelCloser}, nil
}

type recommendCloser struct{ index, model io.Closer }

func (c *recommendCloser) Close() error {
	if c.model != nil {
		_ = c.model.Close()
	}
	if c.index != nil {
		return c.index.Close()
	}
	return nil
}

func buildArticleSearch(cfg config.Config, pool *pgxpool.Pool, observer searchApp.Observer, logger *slog.Logger, sharedReaders ...*articleread.Service) (searchApp.Searcher, io.Closer) {
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
	var reader searchApp.PublicArticleReader = postgres.NewArticleRepository(pool)
	var current searchApp.CurrentArticleReader = postgres.NewArticleRepository(pool)
	if len(sharedReaders) > 0 && sharedReaders[0] != nil {
		reader = sharedReaders[0]
		current = sharedReaders[0]
	}
	service := searchApp.NewService(client, reader, codec, observer, searchApp.Config{
		PITKeepAlive: cfg.Search.Query.PITKeepAlive, CandidateBatchSize: cfg.Search.Query.CandidateBatchSize,
		MaxCandidatesPerRequest: cfg.Search.Query.MaxCandidatesPerRequest,
		Timeout:                 cfg.Search.Query.Timeout,
		Hybrid: searchApp.HybridConfig{Enabled: cfg.Search.Query.Hybrid.Enabled, BM25Candidates: cfg.Search.Query.Hybrid.BM25Candidates, KNNCandidates: cfg.Search.Query.Hybrid.KNNCandidates,
			EmbeddingTimeout: cfg.Search.Query.Hybrid.EmbeddingTimeout, KNNTimeout: cfg.Search.Query.Hybrid.KNNTimeout, Provider: cfg.AI.Embedding.Profile.Provider, Model: cfg.AI.Embedding.Profile.Model, Profile: cfg.AI.Embedding.Profile.ProfileVersion, Dimensions: cfg.AI.Embedding.Dimensions},
	})
	modelCtx, cancelModel := context.WithCancel(context.Background())
	var queryEmbedder searchApp.QueryEmbedder
	var modelCloser io.Closer
	if cfg.Search.Query.Hybrid.Enabled && cfg.AI.Embedding.Profile.Enabled() {
		adapter, err := einoAdapter.NewOpenAIQueryEmbedder(modelCtx, cfg.AI.Embedding, cfg.Search.Query.Hybrid.EmbeddingTimeout)
		if err == nil {
			queryEmbedder = adapter
			modelCloser = adapter
		} else if logger != nil {
			logger.Warn("查询 Embedding 装配失败，降级为 BM25")
		}
	}
	service.WithHybrid(cancellableQueryEmbedder(queryEmbedder, modelCtx), current)
	return service, &searchCloser{client: client, cancel: cancelModel, model: modelCloser}
}

type searchCloser struct {
	client io.Closer
	model  io.Closer
	cancel context.CancelFunc
}

func (c *searchCloser) Close() error {
	c.cancel()
	if c.model != nil {
		_ = c.model.Close()
	}
	return c.client.Close()
}

type lifecycleQueryEmbedder struct {
	embedder searchApp.QueryEmbedder
	ctx      context.Context
}

func cancellableQueryEmbedder(embedder searchApp.QueryEmbedder, ctx context.Context) searchApp.QueryEmbedder {
	if embedder == nil {
		return nil
	}
	return lifecycleQueryEmbedder{embedder, ctx}
}
func (e lifecycleQueryEmbedder) EmbedQuery(ctx context.Context, q string) ([]float64, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(e.ctx, cancel)
	defer stop()
	if e.ctx.Err() != nil {
		cancel()
	}
	return e.embedder.EmbedQuery(ctx, q)
}
