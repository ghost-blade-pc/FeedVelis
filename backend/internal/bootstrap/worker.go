package bootstrap

import (
	"context"
	"log/slog"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

// RunWorker 提供可关闭的 Worker 进程骨架；具体消费者和调度器在后续里程碑注册。
func RunWorker(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	ticker := time.NewTicker(cfg.Worker.HeartbeatInterval)
	defer ticker.Stop()
	logger.Info("velis-worker 已启动", "environment", cfg.App.Environment)

	for {
		select {
		case <-ctx.Done():
			logger.Info("velis-worker 已停止接收新任务")
			return nil
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, cfg.Database.ConnectTimeout)
			err := pool.Ping(pingCtx)
			cancel()
			if err != nil {
				logger.Error("Worker 必要依赖检查失败", "error", err)
				continue
			}
			logger.Debug("Worker heartbeat")
		}
	}
}
