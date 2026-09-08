package bootstrap

import (
	"context"
	"log/slog"
	"os"
	"strconv"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/scheduler"
)

// RunWorker 提供可关闭的 Worker 进程骨架；具体消费者和调度器在后续里程碑注册。
func RunWorker(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	feed := buildFeedServices(pool)
	owner := workerOwner()
	logger.Info("velis-worker 已启动", "environment", cfg.App.Environment, "worker_id", owner)
	err = scheduler.New(feed.sources, logger, owner, cfg.Worker.HeartbeatInterval).Run(ctx)
	logger.Info("velis-worker 已停止接收新任务")
	return err
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
