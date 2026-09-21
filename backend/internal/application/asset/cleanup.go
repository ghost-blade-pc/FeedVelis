package asset

import (
	"context"
	"errors"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	assetDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/asset"
)

// maxCleanupBatchesPerRun 限制单轮清理的批次数，避免积压时长时间占用数据库连接。
const maxCleanupBatchesPerRun = 20

// CleanupRepository 提供对象清理所需的候选查询；实现必须在调用方事务内生效。
type CleanupRepository interface {
	// ListExpiredPending 返回超过保留期仍未确认的待上传资产。
	ListExpiredPending(context.Context, time.Time, int) ([]assetDomain.Asset, error)
	// ListUnboundReady 返回超过保留期仍未绑定文章的已确认资产。
	ListUnboundReady(context.Context, time.Time, int) ([]assetDomain.Asset, error)
	// ListDeletePending 返回已标记待删除、等待对象清理的资产。
	ListDeletePending(context.Context, int) ([]assetDomain.Asset, error)
	Save(context.Context, assetDomain.Asset) error
	// Delete 直接移除从未确认过的资产行：这类资产没有媒体信息，
	// 数据库约束也不允许它进入 delete_pending，清理失败时它仍会因过期被再次选中。
	Delete(context.Context, string) error
}

// CleanupDeps 是资产对象清理的依赖；保留期来自配置。
type CleanupDeps struct {
	Repository CleanupRepository
	Storage    StorageRemover
	Clock      ports.Clock
	Batch      int
	PendingTTL time.Duration
	UnboundTTL time.Duration
}

// StorageRemover 是清理所需的存储能力：只要幂等删除一项。
type StorageRemover interface {
	DeleteObject(context.Context, string) error
}

// CleanupResult 汇总一轮清理的处置数量。
type CleanupResult struct {
	// Marked 是本轮新标记为待删除的资产数（过期待上传与长期未绑定）。
	Marked int64
	// Deleted 是对象已删除并落库为 deleted 的资产数。
	Deleted int64
	// Failed 是对象删除失败、留待下一轮重试的资产数。
	Failed int64
}

func (r CleanupResult) Total() int64 { return r.Marked + r.Deleted + r.Failed }

type CleanupService struct{ deps CleanupDeps }

func NewCleanupService(deps CleanupDeps) (*CleanupService, error) {
	if deps.Repository == nil || deps.Storage == nil || deps.Clock == nil {
		return nil, errors.New("资产清理用例缺少必要依赖")
	}
	if deps.Batch <= 0 {
		return nil, errors.New("资产清理批大小必须为正")
	}
	return &CleanupService{deps: deps}, nil
}

// Run 先标记过期资产，再清理待删除对象。
// 下架不是删除：只有过期的待上传、长期未绑定以及文章软删除三种来源会进入删除流程。
func (s *CleanupService) Run(ctx context.Context) (CleanupResult, error) {
	now := s.deps.Clock.Now().UTC()
	var result CleanupResult
	marked, err := s.mark(ctx, now)
	result.Marked = marked
	if err != nil {
		return result, err
	}
	deleted, failed, err := s.remove(ctx, now)
	result.Deleted, result.Failed = deleted, failed
	return result, err
}

// mark 把长期未绑定的已确认资产标记为待删除。
// 过期待上传资产不进入该状态：它没有媒体信息，数据库约束也不允许它离开 pending。
func (s *CleanupService) mark(ctx context.Context, now time.Time) (int64, error) {
	var marked int64
	for batch := 0; batch < maxCleanupBatchesPerRun; batch++ {
		candidates, err := s.deps.Repository.ListUnboundReady(ctx, now.Add(-s.deps.UnboundTTL), s.deps.Batch)
		if err != nil {
			return marked, err
		}
		if len(candidates) == 0 {
			break
		}
		for _, candidate := range candidates {
			if candidate.Status() == assetDomain.StatusDeletePending {
				continue
			}
			if err := candidate.RequestDelete(now); err != nil {
				return marked, err
			}
			if err := s.deps.Repository.Save(ctx, candidate); err != nil {
				return marked, err
			}
			marked++
		}
		if len(candidates) < s.deps.Batch {
			break
		}
	}
	return marked, nil
}

// remove 逐条删除对象：单条失败只计入 Failed，不中断整轮，使删除可在后续周期重试。
func (s *CleanupService) remove(ctx context.Context, now time.Time) (int64, int64, error) {
	var deleted, failed int64
	deleted, failed, err := s.removeDeletePending(ctx, now, deleted, failed)
	if err != nil {
		return deleted, failed, err
	}
	// 过期待上传资产没有媒体信息，删除对象后直接移除行；
	// 删除失败时行仍是 pending 且已过期，下一轮会再次选中，重试语义不变。
	for batch := 0; batch < maxCleanupBatchesPerRun; batch++ {
		candidates, err := s.deps.Repository.ListExpiredPending(ctx, now.Add(-s.deps.PendingTTL), s.deps.Batch)
		if err != nil {
			return deleted, failed, err
		}
		if len(candidates) == 0 {
			break
		}
		for _, candidate := range candidates {
			if err := s.deps.Storage.DeleteObject(ctx, candidate.ObjectKey()); err != nil {
				failed++
				continue
			}
			if err := s.deps.Repository.Delete(ctx, candidate.ID()); err != nil {
				return deleted, failed, err
			}
			deleted++
		}
		if len(candidates) < s.deps.Batch {
			break
		}
	}
	return deleted, failed, nil
}

func (s *CleanupService) removeDeletePending(ctx context.Context, now time.Time, deleted, failed int64) (int64, int64, error) {
	for batch := 0; batch < maxCleanupBatchesPerRun; batch++ {
		candidates, err := s.deps.Repository.ListDeletePending(ctx, s.deps.Batch)
		if err != nil {
			return deleted, failed, err
		}
		if len(candidates) == 0 {
			break
		}
		for _, candidate := range candidates {
			// 幂等删除：对象已不存在同样视为成功。
			if err := s.deps.Storage.DeleteObject(ctx, candidate.ObjectKey()); err != nil {
				failed++
				continue
			}
			if err := candidate.MarkDeleted(now); err != nil {
				return deleted, failed, err
			}
			if err := s.deps.Repository.Save(ctx, candidate); err != nil {
				return deleted, failed, err
			}
			deleted++
		}
		if len(candidates) < s.deps.Batch {
			break
		}
	}
	return deleted, failed, nil
}
