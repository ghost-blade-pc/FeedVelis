package scheduler

import (
	"context"
	"log/slog"
	"time"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
)

// CleanupRunner 是清理用例在本层消费的接口。
type CleanupRunner interface {
	Run(context.Context) (accountApp.CleanupResult, error)
}

// CleanupScheduler 按周期触发保留期清理，并只以结构化日志字段记录删除数量。
type CleanupScheduler struct {
	runner   CleanupRunner
	logger   *slog.Logger
	interval time.Duration
}

func NewCleanup(runner CleanupRunner, logger *slog.Logger, interval time.Duration) *CleanupScheduler {
	return &CleanupScheduler{runner: runner, logger: logger, interval: interval}
}

// Run 先执行一轮，再按周期执行，直到上下文结束；清理失败只记录日志，不影响其他调度。
func (s *CleanupScheduler) Run(ctx context.Context) {
	s.runOnce(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runOnce(ctx)
		}
	}
}

func (s *CleanupScheduler) runOnce(ctx context.Context) {
	startedAt := time.Now()
	result, err := s.runner.Run(ctx)
	duration := time.Since(startedAt).Milliseconds()
	attributes := []any{
		"operation", "cleanup",
		"duration_ms", duration,
		"deleted_sessions", result.Sessions,
		"deleted_refresh_tokens", result.Tokens,
		"deleted_failures", result.Failures,
		"deleted_blocks", result.Blocks,
	}
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if s.logger != nil {
			s.logger.Error("认证数据清理失败", append(attributes, "result", "failure", "error", err)...)
		}
		return
	}
	if s.logger != nil {
		s.logger.Info("认证数据清理完成", append(attributes, "result", "success")...)
	}
}
