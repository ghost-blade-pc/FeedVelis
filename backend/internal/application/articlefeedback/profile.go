package articlefeedback

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	feedback "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/articlefeedback"
)

type Sample struct {
	ArticleID     int64
	ReadDays      int
	Favorited     bool
	NotInterested bool
	Topics        []string
	SourceID      *int64
}

type ProfileRepository interface {
	Excluded(context.Context, string, []int64, time.Time) ([]int64, error)
	Samples(context.Context, string, time.Time) ([]Sample, error)
}

type ProfileService struct {
	repository ProfileRepository
	clock      Clock
}

func NewProfileService(repository ProfileRepository, clock Clock) *ProfileService {
	return &ProfileService{repository: repository, clock: clock}
}

func (s *ProfileService) Profile(ctx context.Context, userID string, candidates []int64) (recommendation.Profile, error) {
	result := recommendation.Profile{Excluded: map[int64]string{}, Topics: map[string]recommendation.Evidence{}, Sources: map[int64]recommendation.Evidence{}}
	if uuid.Validate(userID) != nil || len(candidates) > 200 {
		return result, feedback.ErrInvalidInput
	}
	seen := make(map[int64]struct{}, len(candidates))
	for _, id := range candidates {
		if id <= 0 {
			return result, feedback.ErrInvalidInput
		}
		if _, ok := seen[id]; ok {
			return result, feedback.ErrInvalidInput
		}
		seen[id] = struct{}{}
	}
	now := s.clock.Now().UTC()
	excluded, err := s.repository.Excluded(ctx, userID, candidates, now)
	if err != nil {
		return result, err
	}
	for _, id := range excluded {
		result.Excluded[id] = recommendation.NotInterestedArticle
	}
	samples, err := s.repository.Samples(ctx, userID, now)
	if err != nil {
		return result, err
	}
	if len(samples) > 500 {
		samples = samples[:500]
	}
	for _, sample := range samples {
		positive := sample.ReadDays
		if positive > 3 {
			positive = 3
		}
		if sample.Favorited {
			positive += 3
		}
		if sample.NotInterested {
			positive = 0
		}
		seenTopics := make(map[string]struct{}, len(sample.Topics))
		for _, topic := range sample.Topics {
			if topic == "" {
				continue
			}
			if _, exists := seenTopics[topic]; exists {
				continue
			}
			seenTopics[topic] = struct{}{}
			item := result.Topics[topic]
			item = addEvidence(item, positive, sample.NotInterested)
			result.Topics[topic] = item
		}
		if sample.SourceID != nil {
			item := result.Sources[*sample.SourceID]
			item = addEvidence(item, positive, sample.NotInterested)
			result.Sources[*sample.SourceID] = item
		}
	}
	for key, item := range result.Topics {
		item.Weight = clampWeight(item.Weight)
		result.Topics[key] = item
	}
	for key, item := range result.Sources {
		item.Weight = clampWeight(item.Weight)
		result.Sources[key] = item
	}
	return result, nil
}

func addEvidence(item recommendation.Evidence, positive int, negative bool) recommendation.Evidence {
	if positive > 0 {
		item.PositiveArticles++
		item.Weight += positive
	}
	if negative {
		item.NegativeArticles++
		item.Weight--
	}
	return item
}

func clampWeight(weight int) int {
	if weight > 20 {
		return 20
	}
	if weight < -20 {
		return -20
	}
	return weight
}
