// Package articleread 批量复核公开事实并装配版本化卡片。
package articleread

import (
	"context"
	"errors"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

var ErrInconsistentFragment = errors.New("公开卡片片段与当前事实不一致")

type CurrentFact struct {
	Identity       articlecache.CardIdentity
	Item           articleDomain.ListItem
	VectorIdentity articlesearch.VectorIdentity
}
type Repository interface {
	ListPublicFacts(context.Context, []int64) ([]CurrentFact, error)
	ListPublicFragments(context.Context, []articlecache.CardIdentity) ([]articlecache.CardFragment, error)
	ListLatestCandidates(context.Context, articlecache.LatestQuery) (articlecache.LatestPage, error)
}

// cacheAllowed 仅在本次拥有的公开只读快照内为 true；已有事务禁止公开缓存读写。
type Snapshot interface {
	WithinReadSnapshot(context.Context, func(context.Context, bool) error) error
}
