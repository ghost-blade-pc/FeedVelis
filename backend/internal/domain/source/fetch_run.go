package source

import (
	"context"
	"errors"
	"strings"
	"time"
)

// FetchTrigger 区分定时调度与管理员手动触发。
type FetchTrigger string

const (
	TriggerScheduled FetchTrigger = "scheduled"
	TriggerManual    FetchTrigger = "manual"
)

// FetchRunStatus 是一次抓取运行的终态集合；running 是唯一的非终态。
type FetchRunStatus string

const (
	RunRunning   FetchRunStatus = "running"
	RunSucceeded FetchRunStatus = "succeeded"
	RunFailed    FetchRunStatus = "failed"
	RunAborted   FetchRunStatus = "aborted"
)

// 历史分页边界：默认 50 条，单页最多 100 条。
const (
	DefaultFetchRunPageSize = 50
	MaxFetchRunPageSize     = 100
)

var (
	ErrInvalidRun        = errors.New("抓取运行无效")
	ErrInvalidRunState   = errors.New("抓取运行状态不允许该操作")
	ErrInvalidPageSize   = errors.New("分页大小无效")
	ErrInvalidRunCursor  = errors.New("抓取历史游标无效")
	maxErrorCodeBytes    = 64
	maxActorIdentitySize = 64
)

// FetchRun 是一次抓取运行的历史记录。设计上只保存计数与受控错误码：
// 完整响应正文、代理凭据与请求头都不进入这张表。
type FetchRun struct {
	ID              string
	SourceID        int64
	Trigger         FetchTrigger
	ActorUserID     *string
	Status          FetchRunStatus
	LeaseGeneration int64
	NotModified     bool
	Inserted        int
	Updated         int
	Unchanged       int
	Skipped         int
	ErrorCode       *string
	StartedAt       time.Time
	CompletedAt     *time.Time
	CreatedAt       time.Time
}

// FetchRunStats 是入库统计；与成功/304 一起构成一次运行的最终结果。
type FetchRunStats struct {
	NotModified bool
	Inserted    int
	Updated     int
	Unchanged   int
	Skipped     int
}

// NewFetchRun 创建一次运行记录。手动触发必须带操作者，租约 generation 必须为正：
// 后者是 fencing 的依据，缺失说明调用方没有真正持有租约。
func NewFetchRun(id string, sourceID int64, trigger FetchTrigger, actorUserID *string, leaseGeneration int64, now time.Time) (FetchRun, error) {
	if strings.TrimSpace(id) == "" || sourceID <= 0 || leaseGeneration <= 0 || now.IsZero() {
		return FetchRun{}, ErrInvalidRun
	}
	switch trigger {
	case TriggerScheduled:
		if actorUserID != nil {
			return FetchRun{}, ErrInvalidRun
		}
	case TriggerManual:
		if actorUserID == nil || strings.TrimSpace(*actorUserID) == "" || len(*actorUserID) > maxActorIdentitySize {
			return FetchRun{}, ErrInvalidRun
		}
	default:
		return FetchRun{}, ErrInvalidRun
	}
	return FetchRun{ID: id, SourceID: sourceID, Trigger: trigger, ActorUserID: copyString(actorUserID),
		Status: RunRunning, LeaseGeneration: leaseGeneration,
		StartedAt: now.UTC(), CreatedAt: now.UTC()}, nil
}

// Restore 从持久化数据重建历史记录，不做校验转换。
func Restore(id string, sourceID int64, trigger FetchTrigger, actorUserID *string, status FetchRunStatus,
	leaseGeneration int64, stats FetchRunStats, errorCode *string,
	startedAt time.Time, completedAt *time.Time, createdAt time.Time) FetchRun {
	return FetchRun{ID: id, SourceID: sourceID, Trigger: trigger, ActorUserID: copyString(actorUserID),
		Status: status, LeaseGeneration: leaseGeneration, NotModified: stats.NotModified,
		Inserted: stats.Inserted, Updated: stats.Updated, Unchanged: stats.Unchanged, Skipped: stats.Skipped,
		ErrorCode: copyString(errorCode), StartedAt: startedAt.UTC(),
		CompletedAt: copyTime(completedAt), CreatedAt: createdAt.UTC()}
}

// Succeed 以成功或 304 结束运行；304 是上游明确告知无变化，仍属于成功。
func (r *FetchRun) Succeed(stats FetchRunStats, completedAt time.Time) error {
	if err := r.requireRunning(completedAt); err != nil {
		return err
	}
	if stats.Inserted < 0 || stats.Updated < 0 || stats.Unchanged < 0 || stats.Skipped < 0 {
		return ErrInvalidRun
	}
	r.Status, r.NotModified = RunSucceeded, stats.NotModified
	r.Inserted, r.Updated, r.Unchanged, r.Skipped = stats.Inserted, stats.Updated, stats.Unchanged, stats.Skipped
	r.ErrorCode = nil
	completed := completedAt.UTC()
	r.CompletedAt = &completed
	return nil
}

// Fail 以稳定错误码结束运行；错误码必须是受控分类，不携带上游原文。
func (r *FetchRun) Fail(errorCode string, completedAt time.Time) error {
	if err := r.requireRunning(completedAt); err != nil {
		return err
	}
	code := strings.TrimSpace(errorCode)
	if code == "" || len(code) > maxErrorCodeBytes {
		return ErrInvalidRun
	}
	r.Status, r.ErrorCode = RunFailed, &code
	completed := completedAt.UTC()
	r.CompletedAt = &completed
	return nil
}

// Abort 收敛崩溃遗留或租约失效的运行，使其不再永久停留在运行中。
func (r *FetchRun) Abort(completedAt time.Time) error {
	if err := r.requireRunning(completedAt); err != nil {
		return err
	}
	r.Status, r.ErrorCode = RunAborted, nil
	completed := completedAt.UTC()
	r.CompletedAt = &completed
	return nil
}

func (r FetchRun) requireRunning(completedAt time.Time) error {
	if r.Status != RunRunning || r.CompletedAt != nil {
		return ErrInvalidRunState
	}
	if completedAt.IsZero() || completedAt.Before(r.StartedAt) {
		return ErrInvalidRun
	}
	return nil
}

// FetchRunCursor 是历史分页的稳定排序键：开始时间倒序，同一时间用运行 ID 决定次序。
type FetchRunCursor struct {
	StartedAt time.Time
	ID        string
}

// NormalizeFetchRunLimit 归一化分页大小：零值取默认，越界即拒绝。
func NormalizeFetchRunLimit(limit int) (int, error) {
	if limit == 0 {
		return DefaultFetchRunPageSize, nil
	}
	if limit < 1 || limit > MaxFetchRunPageSize {
		return 0, ErrInvalidPageSize
	}
	return limit, nil
}

func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

// FetchRunRepository 持久化抓取历史；实现必须只写入计数与受控错误码。
type FetchRunRepository interface {
	// Start 记录一次开始运行的抓取；同一来源同时只允许一个运行中的记录。
	Start(context.Context, FetchRun) error
	// Complete 写入终态与统计；必须携带租约 generation 参与 fencing。
	Complete(context.Context, FetchRun) error
	// AbortStale 把某来源上租约已经失效的运行收敛为中止，返回被收敛的条数。
	AbortStale(context.Context, int64, int64, time.Time) (int64, error)
	// ListBySource 按开始时间倒序翻页；游标由调用方用上一次结果构造。
	ListBySource(context.Context, int64, *FetchRunCursor, int) ([]FetchRun, error)
	// CurrentRunning 返回来源上仍在运行的那一条记录；没有则返回 ErrNotFound。
	// 幂等重试据此把「同一操作的第二次请求」指回同一个运行 ID。
	CurrentRunning(context.Context, int64) (FetchRun, error)
}

func copyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
