package articleread

import (
	"context"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

// ListLatest 的水位包含已检查的不可见候选，但不消费有效 lookahead。
func (s *Service) ListLatest(ctx context.Context, position *articleDomain.Cursor, limit int) ([]articleDomain.ListItem, *articleDomain.Cursor, bool, error) {
	if limit < 1 || limit > 50 {
		return nil, nil, false, articleDomain.ErrInvalidArgument
	}
	ctx = s.NewRequest(ctx)
	items := make([]articleDomain.ListItem, 0, limit)
	checked := 0
	for checked < 500 {
		q := articlecache.LatestQuery{Limit: limit, Position: position}
		page, current, err := s.latestBatch(ctx, q, min(500-checked, limit+1))
		if err != nil {
			return nil, nil, false, err
		}
		byID := map[int64]articleDomain.ListItem{}
		for _, item := range current {
			byID[item.Item.ID] = item.Item
		}
		for i, candidate := range page.Candidates {
			if checked == 500 {
				return items, position, true, nil
			}
			item, visible := byID[candidate.ArticleID]
			if visible && len(items) == limit {
				return items, position, true, nil
			}
			checked++
			position = &articleDomain.Cursor{ArticleID: candidate.ArticleID, SortAt: candidate.SortAt.UTC()}
			if visible {
				items = append(items, item)
			}
			if checked == 500 {
				more := i+1 < len(page.Candidates) || !page.Exhausted
				if !more {
					return items, nil, false, ctx.Err()
				}
				return items, position, true, ctx.Err()
			}
		}
		if page.Exhausted {
			return items, nil, false, ctx.Err()
		}
		if len(page.Candidates) == 0 {
			return nil, nil, false, ErrInconsistentFragment
		}
	}
	return items, position, true, ctx.Err()
}

// 候选和卡片共享同一数据库视图；缓存排序位置变化时整批按原位置重查。
func (s *Service) latestBatch(ctx context.Context, q articlecache.LatestQuery, remaining int) (articlecache.LatestPage, []articlesearch.CurrentArticle, error) {
	var page articlecache.LatestPage
	var current []articlesearch.CurrentArticle
	var writes []articlecache.CardWrite
	var created time.Time
	var fill, allowed bool
	err := s.snapshot.WithinReadSnapshot(ctx, func(view context.Context, cacheAllowed bool) error {
		allowed = cacheAllowed
		hit := false
		if allowed {
			cached, ok, cacheErr := s.cache.GetLatest(view, q)
			if view.Err() != nil {
				return view.Err()
			}
			if cacheErr == nil && ok {
				page = cached
				hit = true
			}
		}
		load := func() error {
			created = s.now().UTC()
			var err error
			page, err = s.repository.ListLatestCandidates(view, q)
			s.observer.Fallback(view, articlecache.Latest, 1)
			fill = allowed
			return err
		}
		if !hit {
			if err := load(); err != nil {
				return err
			}
		}
		assemble := func() error {
			ids := make([]int64, 0, min(remaining, len(page.Candidates)))
			for _, c := range page.Candidates[:min(remaining, len(page.Candidates))] {
				ids = append(ids, c.ArticleID)
			}
			current = nil
			writes = nil
			for start := 0; start < len(ids); start += s.batchSize {
				part, fill, batchErr := s.assemble(view, ids[start:min(start+s.batchSize, len(ids))], allowed)
				if batchErr != nil {
					return batchErr
				}
				current = append(current, part...)
				writes = append(writes, fill...)
			}
			return nil
		}
		if err := assemble(); err != nil {
			return err
		}
		if hit {
			positions := map[int64]time.Time{}
			for _, c := range page.Candidates {
				positions[c.ArticleID] = c.SortAt
			}
			for _, item := range current {
				if !item.Item.SortAt.Equal(positions[item.Item.ID]) {
					if err := load(); err != nil {
						return err
					}
					return assemble()
				}
			}
		}
		return nil
	})
	if err != nil {
		return articlecache.LatestPage{}, nil, err
	}
	if allowed {
		if fill {
			_ = s.cache.PutLatest(ctx, q, page, created)
		}
		if len(writes) > 0 {
			_ = s.cache.PutCards(ctx, writes)
		}
	}
	return page, current, ctx.Err()
}
