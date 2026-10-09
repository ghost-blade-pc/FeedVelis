// Package articlecache 定义可丢弃读取缓存；调用方必须先复核当前公开事实。
package articlecache

import (
	"context"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type CardIdentity struct {
	ArticleID    int64
	RevisionID   int64
	GenerationID string
}

// CardFragment 仅包含不可变修订和 generation 片段，不包含动态昵称、来源或正文。
type CardFragment struct {
	Identity         CardIdentity
	Origin           articleDomain.OriginType
	Title            string
	Excerpt          string
	SourceAuthorName *string
	Enhancement      *articleDomain.Enhancement
}
type CardWrite struct {
	Fragment  CardFragment
	CreatedAt time.Time
}
type LatestQuery struct {
	Limit    int
	Position *articleDomain.Cursor
}
type Candidate struct {
	ArticleID int64
	SortAt    time.Time
}
type LatestPage struct {
	Candidates []Candidate
	Exhausted  bool
}

// 推荐计划类型归推荐用例所有，别名使三类对象共享同一个适配器。
type PlanIdentity = recommendation.PlanIdentity
type RecommendationPlan = recommendation.CachedPlan
type Cache interface {
	NewRequest(context.Context) context.Context
	GetCards(context.Context, []CardIdentity) (map[CardIdentity]CardFragment, error)
	PutCards(context.Context, []CardWrite) error
	GetLatest(context.Context, LatestQuery) (LatestPage, bool, error)
	PutLatest(context.Context, LatestQuery, LatestPage, time.Time) error
	InvalidateLatest(context.Context) error
	GetPlan(context.Context, PlanIdentity) (RecommendationPlan, bool, error)
	PutPlan(context.Context, PlanIdentity, RecommendationPlan, time.Time) error
}

// Disabled 保持父请求取消语义，其余操作直接未命中或成功绕过。
type Disabled struct{}

func (Disabled) NewRequest(ctx context.Context) context.Context { return ctx }
func (Disabled) GetCards(ctx context.Context, _ []CardIdentity) (map[CardIdentity]CardFragment, error) {
	return map[CardIdentity]CardFragment{}, ctx.Err()
}
func (Disabled) PutCards(ctx context.Context, _ []CardWrite) error { return ctx.Err() }
func (Disabled) GetLatest(ctx context.Context, _ LatestQuery) (LatestPage, bool, error) {
	return LatestPage{}, false, ctx.Err()
}
func (Disabled) PutLatest(ctx context.Context, _ LatestQuery, _ LatestPage, _ time.Time) error {
	return ctx.Err()
}
func (Disabled) InvalidateLatest(ctx context.Context) error { return ctx.Err() }
func (Disabled) GetPlan(ctx context.Context, _ PlanIdentity) (RecommendationPlan, bool, error) {
	return RecommendationPlan{}, false, ctx.Err()
}
func (Disabled) PutPlan(ctx context.Context, _ PlanIdentity, _ RecommendationPlan, _ time.Time) error {
	return ctx.Err()
}

type Object string

const (
	Card      Object = "card"
	Latest    Object = "latest"
	Recommend Object = "recommend"
)

type Operation string

const (
	Get        Operation = "get"
	Set        Operation = "set"
	Invalidate Operation = "invalidate"
)

type Result string

const (
	Hit     Result = "hit"
	Miss    Result = "miss"
	Corrupt Result = "corrupt"
	Bypass  Result = "bypass"
	Failure Result = "failure"
	Success Result = "success"
)

type Reason string

const (
	None      Reason = "none"
	Invalid   Reason = "invalid"
	Transport Reason = "transport"
	Timeout   Reason = "timeout"
	Exhausted Reason = "budget"
	Canceled  Reason = "canceled"
)

type Observer interface {
	Count(context.Context, Object, Operation, Result, Reason, int)
	ObserveDuration(context.Context, Object, Operation, Reason, time.Duration)
	Fallback(context.Context, Object, int)
}
type NopObserver struct{}

func (NopObserver) Count(context.Context, Object, Operation, Result, Reason, int)             {}
func (NopObserver) ObserveDuration(context.Context, Object, Operation, Reason, time.Duration) {}
func (NopObserver) Fallback(context.Context, Object, int)                                     {}
