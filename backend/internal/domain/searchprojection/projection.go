// Package searchprojection 定义文章搜索投影的收敛槽位、按物理索引的投递与索引重建阶段。
// 这里只描述「推进到 PostgreSQL 当前事实」的意图，不包含 SQL、HTTP、OpenSearch 或 SDK 类型：
// 索引是可丢弃的派生状态，任何投影结果都不得反向修改业务事实。
package searchprojection

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// MaxErrorCodeLength 与持久化列宽一致：错误码必须是可聚合的稳定分类。
	MaxErrorCodeLength = 64
	// MaxErrorMessageLength 与持久化列宽一致：摘要必须已脱敏并限长。
	MaxErrorMessageLength = 512
	// MaxPhysicalIndexLength 与持久化列宽一致。
	MaxPhysicalIndexLength = 255
)

var (
	// ErrInvalidTarget 表示目标身份不完整或与动作矛盾。
	ErrInvalidTarget = errors.New("投影目标无效")
	// ErrInvalidPhaseTransition 表示重建阶段不能按该顺序转换。
	ErrInvalidPhaseTransition = errors.New("重建阶段转换无效")
)

// Action 是槽位要收敛到的可检索性。
type Action string

const (
	ActionUpsert    Action = "upsert"
	ActionTombstone Action = "tombstone"
)

func (a Action) Valid() bool { return a == ActionUpsert || a == ActionTombstone }

// Target 是槽位必须收敛到的完整当前事实。
// 它决定 generation 是否递增：任一字段变化都算新目标，完全相同的重复推进是 noop。
type Target struct {
	Action             Action
	ArticleLockVersion int64
	RevisionID         int64
	GenerationResultID *string
	EmbeddingResultID  *string
}

// NewTarget 规范化可选标识：空白字符串与 nil 都表示「没有该 AI 结果」。
func NewTarget(action Action, articleLockVersion, revisionID int64, generationResultID, embeddingResultID *string) (Target, error) {
	target := Target{
		Action:             action,
		ArticleLockVersion: articleLockVersion,
		RevisionID:         revisionID,
		GenerationResultID: normalizedIdentifier(generationResultID),
		EmbeddingResultID:  normalizedIdentifier(embeddingResultID),
	}
	if err := target.Validate(); err != nil {
		return Target{}, err
	}
	return target, nil
}

func (t Target) Validate() error {
	if !t.Action.Valid() {
		return ErrInvalidTarget
	}
	if t.ArticleLockVersion <= 0 || t.RevisionID <= 0 {
		return ErrInvalidTarget
	}
	// tombstone 只携带身份与版本，不保留任何 AI 结果引用。
	if t.Action == ActionTombstone && (t.GenerationResultID != nil || t.EmbeddingResultID != nil) {
		return ErrInvalidTarget
	}
	return nil
}

// WithArticleLockVersion 返回替换文章版本后的目标；用于从文章行构造 tombstone 目标。
func (t Target) WithArticleLockVersion(lockVersion int64) Target {
	t.ArticleLockVersion = lockVersion
	return t
}

// Equal 比较规范化后的完整目标身份。
func (t Target) Equal(other Target) bool {
	return t.Action == other.Action &&
		t.ArticleLockVersion == other.ArticleLockVersion &&
		t.RevisionID == other.RevisionID &&
		identifierValue(t.GenerationResultID) == identifierValue(other.GenerationResultID) &&
		identifierValue(t.EmbeddingResultID) == identifierValue(other.EmbeddingResultID)
}

// Status 是收敛槽位以及单个 delivery 的状态。
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusRetryWait Status = "retry_wait"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

// Settled 表示该状态不再需要自动推进。
func (s Status) Settled() bool { return s == StatusSucceeded || s == StatusFailed }

// Failure 是限长、脱敏后的失败诊断。
type Failure struct {
	Code    string
	Message string
}

// NewFailure 截断到与持久化列一致的宽度；调用方负责先移除凭据与正文。
func NewFailure(code, message string) *Failure {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil
	}
	return &Failure{Code: truncate(code, MaxErrorCodeLength), Message: truncate(message, MaxErrorMessageLength)}
}

// Lease 是一次认领的排他凭证；完成与失败写回必须同时匹配 owner、token 与 generation。
type Lease struct {
	Owner     string
	Token     string
	ExpiresAt time.Time
}

// Expired 判断租约在给定时刻是否已失效。
func (l Lease) Expired(now time.Time) bool { return !l.ExpiresAt.After(now) }

// Job 是每篇文章唯一的持久化收敛槽位。
type Job struct {
	ArticleID       int64
	Target          Target
	Generation      int64
	ChangeSeq       int64
	Status          Status
	Attempt         int
	NextAttemptAt   time.Time
	Lease           *Lease
	LastError       *Failure
	TargetChangedAt time.Time
	CompletedAt     *time.Time
	CreatedAt       time.Time
}

// Advance 把槽位推进到 target。
// 目标完全相同时返回原槽位与 false：不消耗 change sequence、不增加 generation，已完成状态保持不变。
// current 为 nil 表示首次为该文章建立槽位，generation 从 1 开始。
func Advance(articleID int64, current *Job, target Target, changeSeq int64, now time.Time) (Job, bool, error) {
	if err := target.Validate(); err != nil {
		return Job{}, false, err
	}
	if articleID <= 0 || changeSeq <= 0 {
		return Job{}, false, ErrInvalidTarget
	}
	now = now.UTC()
	if current == nil {
		return Job{
			ArticleID:       articleID,
			Target:          target,
			Generation:      1,
			ChangeSeq:       changeSeq,
			Status:          StatusPending,
			NextAttemptAt:   now,
			TargetChangedAt: now,
			CreatedAt:       now,
		}, true, nil
	}
	if current.Target.Equal(target) {
		return *current, false, nil
	}
	// 目标变化使旧 generation 的一切执行失效：租约、尝试与失败诊断都重置。
	return Job{
		ArticleID:       articleID,
		Target:          target,
		Generation:      current.Generation + 1,
		ChangeSeq:       changeSeq,
		Status:          StatusPending,
		NextAttemptAt:   now,
		TargetChangedAt: now,
		CreatedAt:       current.CreatedAt,
	}, true, nil
}

// Result 是适配器对一个文档写入结果的分类；它不含协议细节。
type Result string

const (
	ResultCreated    Result = "created"
	ResultUpdated    Result = "updated"
	ResultTombstoned Result = "tombstoned"
	ResultNoop       Result = "noop"
	ResultRetryable  Result = "retryable"
	ResultPermanent  Result = "permanent"
	ResultUnknown    Result = "unknown"
)

// Applied 表示该结果已经让远端收敛，不需要再重试。
func (r Result) Applied() bool {
	switch r {
	case ResultCreated, ResultUpdated, ResultTombstoned, ResultNoop:
		return true
	default:
		return false
	}
}

// Delivery 是槽位在一个物理索引上的投递要求。
// 重建双写时同一槽位会有多条 delivery，重试只重发尚未收敛的那一条。
type Delivery struct {
	ArticleID          int64
	PhysicalIndex      string
	RequiredGeneration int64
	Status             Status
	Attempt            int
	NextAttemptAt      time.Time
	LastResult         Result
	LastError          *Failure
}

// Converged 判断该投递是否已在槽位当前 generation 上收敛。
func (d Delivery) Converged(generation int64) bool {
	return d.Status == StatusSucceeded && d.RequiredGeneration == generation
}

// NextDeliveryStatus 依据适配器返回的分类决定投递的下一步状态。
// 可重试结果在尝试次数用尽后才进入 failed，永久失败立即进入 failed 且不影响其他 delivery。
func NextDeliveryStatus(result Result, attempt, maxAttempts int) Status {
	if result.Applied() {
		return StatusSucceeded
	}
	if result == ResultPermanent {
		return StatusFailed
	}
	if maxAttempts < 1 || attempt >= maxAttempts {
		return StatusFailed
	}
	return StatusRetryWait
}

// ConvergedStatus 汇总槽位状态：只有全部活动 delivery 都在当前 generation 上成功才算成功。
// 没有任何 delivery（例如 OpenSearch 未初始化或未建立索引）时保持待处理：
// 没有索引可写不等于投影已经完成，槽位必须保留待处理目标。
func ConvergedStatus(generation int64, deliveries []Delivery) Status {
	if len(deliveries) == 0 {
		return StatusPending
	}
	lagging, failed, retrying := 0, 0, 0
	for _, delivery := range deliveries {
		if delivery.Converged(generation) {
			continue
		}
		lagging++
		switch delivery.Status {
		case StatusFailed:
			failed++
		case StatusRetryWait:
			retrying++
		}
	}
	switch {
	case lagging == 0:
		return StatusSucceeded
	case failed > 0:
		return StatusFailed
	case retrying == lagging:
		return StatusRetryWait
	default:
		return StatusPending
	}
}

// BackoffDelay 是指数退避：第 n 次尝试等待为 base * 2^(n-1)，并有上限。
func BackoffDelay(base, max time.Duration, attempt int) time.Duration {
	if base <= 0 || attempt < 1 {
		return 0
	}
	delay := base
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= max && max > 0 {
			return max
		}
	}
	if max > 0 && delay > max {
		return max
	}
	return delay
}

func normalizedIdentifier(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func identifierValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// truncate 按字节限长后回退到合法 UTF-8 边界，避免截断出半个字符。
func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
