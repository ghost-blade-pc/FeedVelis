package articleread

import (
	"context"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type Service struct {
	repository Repository
	snapshot   Snapshot
	cache      articlecache.Cache
	observer   articlecache.Observer
	batchSize  int
	now        func() time.Time
}

func NewService(repo Repository, snapshot Snapshot, cache articlecache.Cache, observer articlecache.Observer, batchSize int, now func() time.Time) *Service {
	if cache == nil {
		cache = articlecache.Disabled{}
	}
	if observer == nil {
		observer = articlecache.NopObserver{}
	}
	if batchSize < 1 || batchSize > 100 {
		batchSize = 100
	}
	if now == nil {
		now = time.Now
	}
	return &Service{repo, snapshot, cache, observer, batchSize, now}
}
func (s *Service) NewRequest(ctx context.Context) context.Context { return s.cache.NewRequest(ctx) }
func (s *Service) ListPublishedByIDs(ctx context.Context, ids []int64) ([]articleDomain.ListItem, error) {
	current, err := s.ListPublishedWithIdentity(ctx, ids)
	if err != nil {
		return nil, err
	}
	result := make([]articleDomain.ListItem, 0, len(current))
	for _, item := range current {
		result = append(result, item.Item)
	}
	return result, nil
}
func (s *Service) ListPublishedWithIdentity(ctx context.Context, ids []int64) ([]articlesearch.CurrentArticle, error) {
	if len(ids) > 500 {
		return nil, articleDomain.ErrInvalidArgument
	}
	ctx = s.NewRequest(ctx)
	ordered := make([]int64, 0, len(ids))
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 {
			return nil, articleDomain.ErrInvalidArgument
		}
		if !seen[id] {
			ordered = append(ordered, id)
			seen[id] = true
		}
	}
	result := make([]articlesearch.CurrentArticle, 0, len(ordered))
	for start := 0; start < len(ordered); start += s.batchSize {
		items, err := s.readBatch(ctx, ordered[start:min(len(ordered), start+s.batchSize)])
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
	}
	return result, ctx.Err()
}
func (s *Service) readBatch(ctx context.Context, ids []int64) ([]articlesearch.CurrentArticle, error) {
	result := make([]articlesearch.CurrentArticle, 0, len(ids))
	var writes []articlecache.CardWrite
	var allowCache bool
	err := s.snapshot.WithinReadSnapshot(ctx, func(view context.Context, allowed bool) error {
		allowCache = allowed
		var err error
		result, writes, err = s.assemble(view, ids, allowed)
		return err
	})
	if err != nil {
		return nil, err
	}
	if allowCache && len(writes) > 0 {
		_ = s.cache.PutCards(ctx, writes)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return result, nil
}

func (s *Service) assemble(view context.Context, ids []int64, allowed bool) ([]articlesearch.CurrentArticle, []articlecache.CardWrite, error) {
	result := make([]articlesearch.CurrentArticle, 0, len(ids))
	var writes []articlecache.CardWrite
	facts, err := s.repository.ListPublicFacts(view, ids)
	if err != nil {
		return nil, nil, err
	}
	identities := make([]articlecache.CardIdentity, 0, len(facts))
	factsByID := map[int64]CurrentFact{}
	for _, fact := range facts {
		factsByID[fact.Identity.ArticleID] = fact
		identities = append(identities, fact.Identity)
	}
	fragments := map[articlecache.CardIdentity]articlecache.CardFragment{}
	if allowed && len(identities) > 0 {
		cached, cacheErr := s.cache.GetCards(view, identities)
		if view.Err() != nil {
			return nil, nil, view.Err()
		}
		if cacheErr == nil && cached != nil {
			fragments = cached
		}
	}
	missing := make([]articlecache.CardIdentity, 0, len(facts))
	for _, id := range identities {
		f, ok := fragments[id]
		if !ok || f.Identity != id || f.Origin != factsByID[id.ArticleID].Item.Origin {
			delete(fragments, id)
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		created := s.now().UTC()
		loaded, err := s.repository.ListPublicFragments(view, missing)
		s.observer.Fallback(view, articlecache.Card, 1)
		if err != nil {
			return nil, nil, err
		}
		wanted := map[articlecache.CardIdentity]bool{}
		for _, id := range missing {
			wanted[id] = true
		}
		for _, fragment := range loaded {
			if !wanted[fragment.Identity] {
				return nil, nil, ErrInconsistentFragment
			}
			fragments[fragment.Identity] = fragment
			if allowed {
				writes = append(writes, articlecache.CardWrite{Fragment: fragment, CreatedAt: created})
			}
		}
	}
	for _, id := range ids {
		fact, ok := factsByID[id]
		if !ok {
			continue
		}
		fragment, ok := fragments[fact.Identity]
		if !ok || fragment.Origin != fact.Item.Origin {
			return nil, nil, ErrInconsistentFragment
		}
		item := fact.Item
		item.RevisionID = fact.Identity.RevisionID
		item.Title = fragment.Title
		item.Excerpt = fragment.Excerpt
		item.Enhancement = fragment.Enhancement
		if item.Origin == articleDomain.OriginRSS {
			item.AuthorName = fragment.SourceAuthorName
		}
		result = append(result, articlesearch.CurrentArticle{Item: item, Identity: fact.VectorIdentity})
	}
	return result, writes, nil
}

func (s *Service) CacheAllowed(ctx context.Context) bool {
	if guard, ok := s.snapshot.(interface{ CacheAllowed(context.Context) bool }); ok {
		return guard.CacheAllowed(ctx)
	}
	return true
}
