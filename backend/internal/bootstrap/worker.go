package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	assetApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asset"
	asyncApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asynctask"
	outboxApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/outbox"
	relayApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/relay"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/clock"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/fetcher/httpfeed"
	rabbitAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/messaging/rabbitmq"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/observability"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/scheduler"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// RunWorker 把各后台能力作为独立受监管组件运行；单个 MQ 组件故障不会停止 Feed。
func RunWorker(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	owner := workerOwner()
	components, err := buildWorkerComponents(cfg, pool, logger, owner)
	if err != nil {
		return err
	}
	feedMode, feedProxyHost := httpfeed.NetworkMode(cfg.Feed.ProxyURL)
	logger.Info("velis-worker 已启动", "environment", cfg.App.Environment, "worker_id", owner,
		"component_count", len(components), "rabbitmq_enabled", cfg.RabbitMQ.URL != "", "feed_network_mode", feedMode, "feed_proxy_host", feedProxyHost)
	if feedMode == "trusted_proxy" {
		// 明确声明信任边界：代理模式下最终地址安全由出口代理负责，应用不做最终 IP 校验。
		logger.Warn("Feed 抓取使用可信出口代理，最终目标地址安全由该代理负责",
			"feed_proxy_host", feedProxyHost)
	}

	err = NewSupervisor(components, logger, time.Second, cfg.Worker.ShutdownTimeout).Run(ctx)
	logger.Info("velis-worker 已停止接收新任务")
	return err
}

func buildWorkerComponents(cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger, owner string) ([]WorkerComponent, error) {
	feed := buildFeedServices(pool, cfg.Feed.ProxyURL)
	registry := prometheus.NewRegistry()
	asyncMetrics := observability.NewAsyncMetrics(registry)
	components := []WorkerComponent{instrumentWorkerComponent("feed", false, asyncMetrics, scheduler.New(feed.sources, logger, owner, cfg.Worker.HeartbeatInterval).Run)}
	cleanup, err := buildCleanupScheduler(cfg, pool, logger)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		components = append(components, instrumentWorkerComponent("cleanup", false, asyncMetrics, func(ctx context.Context) error { cleanup.Run(ctx); return nil }))
	}
	relayRepository := postgres.NewRelayRepository(pool)
	outboxCleanup := outboxApp.NewCleanup(observability.InstrumentOutboxMaintenance(relayRepository, asyncMetrics), cfg.Outbox.Retention, cfg.Outbox.CleanupBatch, cfg.Outbox.CleanupInterval, nil)
	components = append(components, instrumentWorkerComponent("outbox-cleanup", false, asyncMetrics, outboxCleanup.Run))
	metrics := observability.NewMetricsServer(cfg.Worker.MetricsAddress, registry)
	components = append(components, instrumentWorkerComponent("metrics", false, asyncMetrics, metrics.Run))
	if cfg.RabbitMQ.URL == "" {
		return components, nil
	}
	relayStore := observability.InstrumentRelayStore(relayRepository, asyncMetrics)
	components = append(components, instrumentWorkerComponent("relay", true, asyncMetrics, func(ctx context.Context) error {
		publisher, dialErr := rabbitAdapter.DialPublisher(cfg.RabbitMQ.URL)
		if dialErr != nil {
			return dialErr
		}
		defer publisher.Close()
		service := relayApp.NewService(relayStore, observability.InstrumentPublisher(publisher, asyncMetrics), relayApp.Config{Owner: owner, BatchSize: cfg.Relay.BatchSize, PublishWindow: cfg.Relay.PublishWindow, Lease: cfg.Relay.Lease, ConfirmTimeout: cfg.Relay.ConfirmTimeout, ScanInterval: cfg.Relay.ScanInterval, BackoffMin: cfg.Relay.BackoffMin, BackoffMax: cfg.Relay.BackoffMax, Jitter: randomJitter})
		return service.Run(ctx)
	}))
	inbox, err := postgres.NewConsumedEventRepository(asyncApp.ConsumerName)
	if err != nil {
		return nil, err
	}
	projector := asyncApp.NewService(postgres.NewTxManager(pool), inbox, postgres.NewAsyncTaskRepository(), nil).WithLogger(logger, owner)
	instrumentedProjector := observability.InstrumentProjector(projector, asyncMetrics)
	components = append(components, instrumentWorkerComponent("consumer", true, asyncMetrics, rabbitAdapter.NewConsumer(cfg.RabbitMQ.URL, cfg.Consumer.Prefetch, instrumentedProjector, isDatabaseFailure).Run))
	return components, nil
}

func instrumentWorkerComponent(name string, reconnect bool, metrics *observability.AsyncMetrics, run func(context.Context) error) WorkerComponent {
	var attempts atomic.Int64
	metricName := strings.ReplaceAll(name, "-", "_")
	return WorkerComponent{Name: name, Run: func(ctx context.Context) error {
		if reconnect && attempts.Add(1) > 1 {
			metrics.MQReconnects.Inc()
		}
		metrics.ComponentUp.WithLabelValues(metricName).Set(1)
		defer metrics.ComponentUp.WithLabelValues(metricName).Set(0)
		return run(ctx)
	}}
}

func isDatabaseFailure(err error) bool {
	var connectErr *pgconn.ConnectError
	var networkErr net.Error
	return errors.As(err, &connectErr) || errors.As(err, &networkErr)
}

// buildCleanupScheduler 装配保留期清理；认证关闭时不启动清理。
// auth.enabled=false 时清理配置不参与校验，间隔为零值，直接使用会构造出无效的定时器。
func buildCleanupScheduler(cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) (*scheduler.CleanupScheduler, error) {
	if !cfg.Auth.Enabled {
		return nil, nil
	}
	service, err := accountApp.NewCleanupService(accountApp.CleanupDeps{
		Repository: postgres.NewCleanupRepository(pool),
		Clock:      clock.System{},
		Batch:      cfg.Auth.CleanupBatch,
	})
	if err != nil {
		return nil, err
	}
	cleanup := scheduler.NewCleanup(service, logger, cfg.Auth.CleanupInterval)
	// 资产对象清理复用同一周期；未配置对象存储时该阶段整体跳过。
	store, err := buildAssetStore(cfg)
	if err != nil {
		logger.Warn("资产存储不可用，本轮不执行对象清理", "error", err)
		return cleanup, nil
	}
	if store == nil {
		return cleanup, nil
	}
	assets, err := assetApp.NewCleanupService(assetApp.CleanupDeps{
		Repository: postgres.NewArticleAssetRepository(pool),
		Storage:    store,
		Clock:      clock.System{},
		Batch:      cfg.Auth.CleanupBatch,
		PendingTTL: cfg.Assets.PendingTTL,
		UnboundTTL: cfg.Assets.UnboundTTL,
	})
	if err != nil {
		return nil, err
	}
	return cleanup.WithAssets(assets), nil
}

func workerOwner() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "worker"
	}
	value := hostname + "-" + strconv.Itoa(os.Getpid())
	if len(value) > 128 {
		value = value[:128]
	}
	return value
}
