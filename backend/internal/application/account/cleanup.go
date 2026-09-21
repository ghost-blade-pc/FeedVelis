package account

import (
	"context"
	"errors"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
)

// 保留期按 spec 固定：登录失败事件 30 分钟；过期限制再保留 24 小时；
// 会话与刷新令牌在过期或撤销后保留 7 天。用户与管理审计不参与清理。
const (
	FailureRetention = 30 * time.Minute
	BlockRetention   = 24 * time.Hour
	SessionRetention = 7 * 24 * time.Hour

	// maxCleanupBatchesPerRun 限制单轮清理的批次数，避免积压时长时间占用数据库连接。
	maxCleanupBatchesPerRun = 20
)

// CleanupDeps 是清理用例的依赖；Batch 是每批最多删除的行数。
type CleanupDeps struct {
	Repository accountDomain.CleanupRepository
	Clock      ports.Clock
	Batch      int
}

// CleanupService 按保留期分批清理会话、刷新令牌与登录限流状态，由 Worker 周期调用。
type CleanupService struct{ deps CleanupDeps }

func NewCleanupService(deps CleanupDeps) (*CleanupService, error) {
	if deps.Repository == nil || deps.Clock == nil {
		return nil, errors.New("清理用例缺少必要依赖")
	}
	if deps.Batch <= 0 {
		return nil, errors.New("清理批大小必须为正")
	}
	return &CleanupService{deps: deps}, nil
}

// CleanupResult 汇总一轮清理各分类实际删除的行数，供调用方记录结构化日志字段。
type CleanupResult struct {
	Sessions    int64
	Tokens      int64
	Failures    int64
	Blocks      int64
	Idempotency int64
}

func (r CleanupResult) Total() int64 {
	return r.Sessions + r.Tokens + r.Failures + r.Blocks + r.Idempotency
}

// Run 依次清理四类数据；任一类失败立即停止并返回已完成的部分结果，便于调用方留证。
func (s *CleanupService) Run(ctx context.Context) (CleanupResult, error) {
	now := s.deps.Clock.Now().UTC()
	var result CleanupResult
	targets := []struct {
		cutoff  time.Time
		remove  func(context.Context, time.Time, int) (int64, error)
		counter *int64
	}{
		{now.Add(-SessionRetention), s.deps.Repository.DeleteExpiredSessions, &result.Sessions},
		{now.Add(-SessionRetention), s.deps.Repository.DeleteExpiredRefreshTokens, &result.Tokens},
		{now.Add(-FailureRetention), s.deps.Repository.DeleteStaleFailures, &result.Failures},
		{now.Add(-BlockRetention), s.deps.Repository.DeleteExpiredBlocks, &result.Blocks},
		{now, s.deps.Repository.DeleteExpiredIdempotency, &result.Idempotency},
	}
	for _, target := range targets {
		removed, err := s.drain(ctx, target.cutoff, target.remove)
		if err != nil {
			return result, err
		}
		*target.counter = removed
	}
	return result, nil
}

// drain 反复取批，直到某批不足一批（说明已清空）或达到单轮批次数上限。
func (s *CleanupService) drain(ctx context.Context, cutoff time.Time, remove func(context.Context, time.Time, int) (int64, error)) (int64, error) {
	var total int64
	for batch := 0; batch < maxCleanupBatchesPerRun; batch++ {
		removed, err := remove(ctx, cutoff, s.deps.Batch)
		if err != nil {
			return total, err
		}
		total += removed
		if removed < int64(s.deps.Batch) {
			break
		}
	}
	return total, nil
}
