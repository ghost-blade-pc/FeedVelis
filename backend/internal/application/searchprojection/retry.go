package searchprojection

import (
	"context"
	"errors"
	"time"

	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
)

// MaxRetryLimit 是单次重试命令的数量上限：管理操作必须是有界的。
const MaxRetryLimit = 1000

// ErrInvalidRetryScope 表示重试范围为空或超出上限。
var ErrInvalidRetryScope = errors.New("失败投影重试范围无效")

// RetryScope 是重试的精确范围；ArticleID 与 Index 为空表示不限定该维度。
type RetryScope struct {
	ArticleID int64
	Index     string
	Limit     int
}

// Valid 校验范围：必须有明确上限，文章 ID 不得为负数。
func (s RetryScope) Valid() bool {
	return s.Limit >= 1 && s.Limit <= MaxRetryLimit && s.ArticleID >= 0 && len(s.Index) <= projectionDomain.MaxPhysicalIndexLength
}

// RetryReport 是可审计的重试报告：它只包含计数与身份，不含正文或错误原文。
type RetryReport struct {
	Scope       RetryScope
	Reactivated []RetriedDelivery
	FailedTotal int64
	Superseded  int64
	Remaining   int64
	RunAt       time.Time
}

// RetriedDelivery 是被重新激活的精确目标。
type RetriedDelivery struct {
	ArticleID  int64
	Index      string
	Generation int64
}

// RetryStore 是失败 delivery 的只写重试端口。
// 它必须在同一条语句内复核 generation 与状态，避免覆盖已推进的新目标。
type RetryStore interface {
	// Reactivate 只重新激活仍处于当前 generation 的失败 delivery，并返回可审计报告。
	Reactivate(context.Context, RetryScope, time.Time) (RetryReport, error)
}

// RetryService 提供一个有界的失败重试管理入口。
type RetryService struct{ store RetryStore }

func NewRetryService(store RetryStore) *RetryService { return &RetryService{store: store} }

// Retry 按精确范围重新激活失败 delivery。
// 活动的新 generation 不会被旧失败覆盖：generation 已在读取后变化的 delivery 不会被匹配。
func (s *RetryService) Retry(ctx context.Context, scope RetryScope, now time.Time) (RetryReport, error) {
	if !scope.Valid() {
		return RetryReport{}, ErrInvalidRetryScope
	}
	return s.store.Reactivate(ctx, scope, now.UTC())
}
