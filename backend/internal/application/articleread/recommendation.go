package articleread

import (
	"context"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type RecommendationRepository interface {
	ListRecommendationCandidates(context.Context, *articleDomain.Cursor, int, []int64, string, time.Time, time.Time) ([]articlecache.Candidate, error)
}

// ListRecommendationLatest 使用专用本人排除和首查水位，不复用匿名 latest ID 页。
func (s *Service) ListRecommendationLatest(ctx context.Context, position *articleDomain.Cursor, limit int, skip []int64, userID string, now, started time.Time) ([]articleDomain.ListItem, error) {
	if limit < 1 || limit > 51 || len(skip) > 200 {
		return nil, articleDomain.ErrInvalidArgument
	}
	repository, ok := s.repository.(RecommendationRepository)
	if !ok {
		return nil, articleDomain.ErrInvalidArgument
	}
	ctx = s.NewRequest(ctx)
	items := []articleDomain.ListItem{}
	var writes []articlecache.CardWrite
	var allowed bool
	err := s.snapshot.WithinReadSnapshot(ctx, func(view context.Context, cacheAllowed bool) error {
		allowed = cacheAllowed
		candidates, err := repository.ListRecommendationCandidates(view, position, limit, skip, userID, now, started)
		if err != nil {
			return err
		}
		ids := make([]int64, 0, len(candidates))
		for _, candidate := range candidates {
			ids = append(ids, candidate.ArticleID)
		}
		for start := 0; start < len(ids); start += s.batchSize {
			current, fill, err := s.assemble(view, ids[start:min(start+s.batchSize, len(ids))], allowed)
			if err != nil {
				return err
			}
			writes = append(writes, fill...)
			for _, item := range current {
				items = append(items, item.Item)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if allowed && len(writes) > 0 {
		_ = s.cache.PutCards(ctx, writes)
	}
	return items, ctx.Err()
}
