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
	Keywords      []string
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

// Exclusions 独立重查当前有效文章级负反馈，不重新读取或冻结偏好样本。
func (s *ProfileService) Exclusions(ctx context.Context, userID string, candidates []int64) (map[int64]string, error) {
	result := map[int64]string{}
	if uuid.Validate(userID) != nil || len(candidates) > 200 {
		return nil, feedback.ErrInvalidInput
	}
	seen := map[int64]bool{}
	for _, id := range candidates {
		if id <= 0 || seen[id] {
			return nil, feedback.ErrInvalidInput
		}
		seen[id] = true
	}
	ids, err := s.repository.Excluded(ctx, userID, candidates, s.clock.Now().UTC())
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		result[id] = recommendation.NotInterestedArticle
	}
	return result, nil
}

func (s *ProfileService) Profile(ctx context.Context, userID string, candidates []int64) (recommendation.Profile, error) {
	result := recommendation.Profile{Excluded: map[int64]string{}, Keywords: map[string]recommendation.Evidence{}, Topics: map[string]recommendation.Evidence{}, Sources: map[int64]recommendation.Evidence{}}
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
	seenSamples := make(map[int64]struct{}, len(samples))
	for _, sample := range samples {
		if _, exists := seenSamples[sample.ArticleID]; exists {
			continue
		}
		seenSamples[sample.ArticleID] = struct{}{}
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
		seenKeywords := make(map[string]struct{}, len(sample.Keywords))
		for _, keyword := range sample.Keywords {
			if keyword == "" {
				continue
			}
			if _, exists := seenKeywords[keyword]; exists {
				continue
			}
			seenKeywords[keyword] = struct{}{}
			result.Keywords[keyword] = addEvidence(result.Keywords[keyword], positive, sample.NotInterested)
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
	for key, item := range result.Keywords {
		item.Weight = clampWeight(item.Weight)
		item.PositiveWeight = clampWeight(item.PositiveWeight)
		item.NegativeWeight = clampWeight(item.NegativeWeight)
		result.Keywords[key] = item
	}
	for key, item := range result.Topics {
		item.Weight = clampWeight(item.Weight)
		item.PositiveWeight = clampWeight(item.PositiveWeight)
		item.NegativeWeight = clampWeight(item.NegativeWeight)
		result.Topics[key] = item
	}
	for key, item := range result.Sources {
		item.Weight = clampWeight(item.Weight)
		item.PositiveWeight = clampWeight(item.PositiveWeight)
		item.NegativeWeight = clampWeight(item.NegativeWeight)
		result.Sources[key] = item
	}
	return result, nil
}

func addEvidence(item recommendation.Evidence, positive int, negative bool) recommendation.Evidence {
	if positive > 0 {
		item.PositiveArticles++
		item.PositiveWeight += positive
		item.Weight += positive
	}
	if negative {
		item.NegativeArticles++
		item.NegativeWeight++
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
