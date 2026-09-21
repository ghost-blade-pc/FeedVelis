package scheduler

import (
	"context"
	"log/slog"
	"time"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	assetApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asset"
)

// CleanupRunner 是清理用例在本层消费的接口。
type CleanupRunner interface {
	Run(context.Context) (accountApp.CleanupResult, error)
}

// AssetCleanupRunner 是资产对象清理用例在本层消费的接口。
type AssetCleanupRunner interface {
	Run(context.Context) (assetApp.CleanupResult, error)
}

// CleanupScheduler 按周期触发保留期清理，并只以结构化日志字段记录删除数量。
// 资产对象清理与认证数据清理共用同一周期，避免为同一目的引入第二个定时器。
type CleanupScheduler struct {
	runner   CleanupRunner
	assets   AssetCleanupRunner
	logger   *slog.Logger
	interval time.Duration
}

func NewCleanup(runner CleanupRunner, logger *slog.Logger, interval time.Duration) *CleanupScheduler {
	return &CleanupScheduler{runner: runner, logger: logger, interval: interval}
}

// WithAssets 追加资产对象清理；未装配对象存储时保持为空，该阶段整体跳过。
func (s *CleanupScheduler) WithAssets(runner AssetCleanupRunner) *CleanupScheduler {
	s.assets = runner
	return s
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
		"deleted_idempotency", result.Idempotency,
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
	if s.assets != nil {
		s.cleanupAssets(ctx, attributes)
	}
	if s.logger != nil {
		s.logger.Info("认证数据清理完成", append(attributes, "result", "success")...)
	}
}

// cleanupAssets 清理孤儿与待删除对象；失败只记录字段，不使整轮清理标记为失败。
func (s *CleanupScheduler) cleanupAssets(ctx context.Context, attributes []any) {
	result, err := s.assets.Run(ctx)
	attributes = append(attributes,
		"marked_assets", result.Marked, "deleted_assets", result.Deleted, "failed_assets", result.Failed)
	if err != nil && ctx.Err() == nil && s.logger != nil {
		s.logger.Error("资产对象清理失败", "result", "failure", "error", err)
	}
}
