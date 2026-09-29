package recommendation

import (
	"math"
	"sort"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

const RankingVersion = 1

type RankedItem struct {
	ID       int64
	Reason   string
	Identity articlesearch.VectorIdentity
	Semantic bool
	Score    float64
	SortAt   time.Time
	SourceID int64
}

// RankV1 使用固定权重及逐步来源惩罚，返回冻结的全序。
func RankV1(candidates []articlesearch.RankedCandidate, current map[int64]articlesearch.CurrentArticle, profile Profile, now time.Time) []RankedItem {
	remaining := make([]RankedItem, 0, len(candidates))
	for _, candidate := range candidates {
		item, ok := current[candidate.ArticleID]
		if !ok || profile.Excluded[candidate.ArticleID] != "" {
			continue
		}
		ageDays := now.UTC().Sub(item.Item.SortAt.UTC()).Hours() / 24
		if ageDays < 0 {
			ageDays = 0
		}
		if ageDays > 3650 {
			ageDays = 3650
		}
		score := 60*candidate.RRFScore + 0.3*math.Exp(-ageDays/14)
		var topicPositive, keywordPositive float64
		if item.Item.Enhancement != nil {
			for _, value := range uniqueValues(item.Item.Enhancement.Topics) {
				positive, negative := evidenceParts(profile.Topics[value])
				contribution := 0.6 * float64(positive) / 20
				score += contribution - 0.25*float64(negative)/20
				topicPositive += contribution
			}
			for _, value := range uniqueValues(item.Item.Enhancement.Keywords) {
				positive, negative := evidenceParts(profile.Keywords[value])
				contribution := 0.4 * float64(positive) / 20
				score += contribution - 0.2*float64(negative)/20
				keywordPositive += contribution
			}
		}
		sourceID := int64(0)
		if item.Item.Origin == articleDomain.OriginRSS {
			sourceID = item.Item.Source.ID
			positive, negative := evidenceParts(profile.Sources[sourceID])
			score += 0.15*float64(positive)/20 - 0.2*float64(negative)/20
		}
		reason := "recent"
		if candidate.Semantic {
			reason = "similar_content"
		}
		if keywordPositive > 0 && keywordPositive >= topicPositive {
			reason = "keyword_match"
		} else if topicPositive > 0 {
			reason = "topic_match"
		}
		remaining = append(remaining, RankedItem{ID: candidate.ArticleID, Score: score, SortAt: item.Item.SortAt, SourceID: sourceID, Reason: reason, Semantic: candidate.Semantic, Identity: candidate.Identity})
	}
	ordered := make([]RankedItem, 0, len(remaining))
	lastSource := int64(0)
	for len(remaining) > 0 {
		best := 0
		for i := 1; i < len(remaining); i++ {
			if ranksBefore(remaining[i], remaining[best], lastSource) {
				best = i
			}
		}
		chosen := remaining[best]
		ordered = append(ordered, chosen)
		lastSource = chosen.SourceID
		remaining = append(remaining[:best], remaining[best+1:]...)
	}
	return ordered
}

func ranksBefore(a, b RankedItem, previousSource int64) bool {
	aScore, bScore := a.Score, b.Score
	if previousSource != 0 && a.SourceID == previousSource {
		aScore -= 0.35
	}
	if previousSource != 0 && b.SourceID == previousSource {
		bScore -= 0.35
	}
	if aScore != bScore {
		return aScore > bScore
	}
	if !a.SortAt.Equal(b.SortAt) {
		return a.SortAt.After(b.SortAt)
	}
	return a.ID > b.ID
}

func bounded(weight int) int {
	if weight > 20 {
		return 20
	}
	if weight < -20 {
		return -20
	}
	return weight
}

func evidenceParts(item Evidence) (positive, negative int) {
	if item.PositiveWeight != 0 || item.NegativeWeight != 0 {
		return bounded(item.PositiveWeight), bounded(item.NegativeWeight)
	}
	if item.Weight > 0 {
		return bounded(item.Weight), 0
	}
	return 0, -bounded(item.Weight)
}

func uniqueValues(values []string) []string {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
