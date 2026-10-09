package redis

import (
	"encoding/hex"
	"strings"
	"unicode/utf8"

	cacheApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/google/uuid"
)

func validUUID(v string) bool {
	id, err := uuid.Parse(v)
	return err == nil && id != uuid.Nil && id.String() == v
}
func validHash(v string) bool {
	b, err := hex.DecodeString(v)
	return err == nil && len(b) == 32 && strings.ToLower(v) == v
}
func validCardIdentity(id cacheApp.CardIdentity) bool {
	return id.ArticleID > 0 && id.RevisionID > 0 && (id.GenerationID == "" || validUUID(id.GenerationID))
}
func boundedText(s string, n int) bool { return utf8.ValidString(s) && utf8.RuneCountInString(s) <= n }
func validFragment(f cacheApp.CardFragment) bool {
	if !validCardIdentity(f.Identity) || f.Origin != articleDomain.OriginRSS && f.Origin != articleDomain.OriginUser || f.Title == "" || !boundedText(f.Title, articleDomain.MaxTitleRunes) || !boundedText(f.Excerpt, articleDomain.MaxExcerptRunes) {
		return false
	}
	if f.SourceAuthorName != nil && (!boundedText(*f.SourceAuthorName, articleDomain.MaxAuthorRunes) || f.Origin != articleDomain.OriginRSS) {
		return false
	}
	if (f.Identity.GenerationID == "") != (f.Enhancement == nil) {
		return false
	}
	if e := f.Enhancement; e != nil {
		if e.GeneratedAt.IsZero() || e.Method != "model" && e.Method != "extractive" || e.Summary == "" || !boundedText(e.Summary, 16000) || len(e.Keywords) > 100 || len(e.Topics) > 100 {
			return false
		}
		for _, labels := range [][]string{e.Keywords, e.Topics} {
			seen := map[string]bool{}
			for _, label := range labels {
				if label == "" || !boundedText(label, 64) || seen[label] || strings.TrimSpace(label) != label {
					return false
				}
				seen[label] = true
			}
		}
	}
	return true
}
func validQuery(q cacheApp.LatestQuery) bool {
	return q.Limit >= 1 && q.Limit <= 50 && (q.Position == nil || q.Position.ArticleID > 0 && !q.Position.SortAt.IsZero())
}
func validPage(q cacheApp.LatestQuery, p cacheApp.LatestPage) bool {
	if len(p.Candidates) > q.Limit+1 || len(p.Candidates) == 0 && !p.Exhausted {
		return false
	}
	previous := q.Position
	seen := map[int64]bool{}
	for _, item := range p.Candidates {
		if item.ArticleID <= 0 || item.SortAt.IsZero() || seen[item.ArticleID] {
			return false
		}
		seen[item.ArticleID] = true
		if previous != nil && (!item.SortAt.Before(previous.SortAt) && !(item.SortAt.Equal(previous.SortAt) && item.ArticleID < previous.ArticleID)) {
			return false
		}
		previous = &articleDomain.Cursor{ArticleID: item.ArticleID, SortAt: item.SortAt}
	}
	return true
}
func validPlanIdentity(id cacheApp.PlanIdentity) bool {
	return validUUID(id.UserID) && validHash(id.ProfileHash) && validHash(id.ConfigHash)
}
func validPlan(p cacheApp.RecommendationPlan) bool {
	if len(p.Items) > 200 || len(p.OriginalIDs) > 200 || !validHash(p.ExclusionHash) || p.RankingVersion != recommendation.RankingVersion || len(p.VectorProfile) > 128 {
		return false
	}
	if p.Degraded {
		if p.DegradeReason != "semantic_unavailable" {
			return false
		}
	} else if p.DegradeReason != "" {
		return false
	}
	original := map[int64]bool{}
	for _, id := range p.OriginalIDs {
		if id <= 0 || original[id] {
			return false
		}
		original[id] = true
	}
	seen := map[int64]bool{}
	for _, item := range p.Items {
		if !original[item.ID] || seen[item.ID] {
			return false
		}
		seen[item.ID] = true
		switch item.Reason {
		case "keyword_match", "topic_match", "similar_content", "recent":
		default:
			return false
		}
		if item.Semantic {
			if p.VectorProfile == "" || item.Identity.RevisionID <= 0 || !validUUID(item.Identity.GenerationID) || !validUUID(item.Identity.EmbeddingID) {
				return false
			}
		} else if item.Identity != (recommendation.FrozenIdentity{}) {
			return false
		}
	}
	return true
}
