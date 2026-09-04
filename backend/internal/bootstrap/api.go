// Package bootstrap 是进程的 Composition Root，可以装配所有层。
package bootstrap

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	hertzhttp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz"
)

func RunAPI(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	healthService := health.NewService(pool)
	h := hertzhttp.NewServer(cfg.HTTP.Address, cfg.HTTP.ShutdownTimeout, logger, healthService)
	errCh := make(chan error, 1)
	go func() {
		errCh <- h.Run()
	}()

	logger.Info("velis-api 已启动",
		"address", cfg.HTTP.Address,
		"environment", cfg.App.Environment,
		"database_max_connections", cfg.Database.MaxConnections,
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
