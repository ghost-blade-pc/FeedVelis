package bootstrap

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"sync"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	assetApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asset"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/clock"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/fetcher/httpfeed"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/scheduler"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RunWorker 装配抓取调度与认证数据清理；两者共用进程生命周期，任一方退出即关闭 Worker。
func RunWorker(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	feed := buildFeedServices(pool, cfg.Feed.ProxyURL)
	owner := workerOwner()
	cleanup, err := buildCleanupScheduler(cfg, pool, logger)
	if err != nil {
		return err
	}
	feedMode, feedProxyHost := httpfeed.NetworkMode(cfg.Feed.ProxyURL)
	logger.Info("velis-worker 已启动", "environment", cfg.App.Environment, "worker_id", owner,
		"cleanup_enabled", cleanup != nil, "feed_network_mode", feedMode, "feed_proxy_host", feedProxyHost)
	if feedMode == "trusted_proxy" {
		// 明确声明信任边界：代理模式下最终地址安全由出口代理负责，应用不做最终 IP 校验。
		logger.Warn("Feed 抓取使用可信出口代理，最终目标地址安全由该代理负责",
			"feed_proxy_host", feedProxyHost)
	}

	var (
		wait    sync.WaitGroup
		feedErr error
	)
	wait.Add(1)
	go func() {
		defer wait.Done()
		feedErr = scheduler.New(feed.sources, logger, owner, cfg.Worker.HeartbeatInterval).Run(ctx)
	}()
	if cleanup != nil {
		wait.Add(1)
		go func() {
			defer wait.Done()
			cleanup.Run(ctx)
		}()
	}
	wait.Wait()
	logger.Info("velis-worker 已停止接收新任务")
	return feedErr
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
