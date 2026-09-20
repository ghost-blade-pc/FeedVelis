package bootstrap

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"sync"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/clock"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
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

	feed := buildFeedServices(pool)
	owner := workerOwner()
	cleanup, err := buildCleanupScheduler(cfg, pool, logger)
	if err != nil {
		return err
	}
	logger.Info("velis-worker 已启动", "environment", cfg.App.Environment, "worker_id", owner,
		"cleanup_enabled", cleanup != nil)

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
	return scheduler.NewCleanup(service, logger, cfg.Auth.CleanupInterval), nil
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
