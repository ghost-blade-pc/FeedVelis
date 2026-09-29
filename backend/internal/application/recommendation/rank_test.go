package recommendation

import (
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

func TestRankV1PreferencesExclusionAndSourceDiversification(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	candidates := []articlesearch.RankedCandidate{}
	current := map[int64]articlesearch.CurrentArticle{}
	for id := int64(1); id <= 4; id++ {
		candidates = append(candidates, articlesearch.RankedCandidate{Candidate: articlesearch.Candidate{ArticleID: id}, RRFScore: 1.0 / 61})
		current[id] = articlesearch.CurrentArticle{Item: articleDomain.ListItem{ID: id, Origin: articleDomain.OriginRSS, SortAt: now, Source: articleDomain.SourceSummary{ID: id}, Enhancement: &articleDomain.Enhancement{Keywords: []string{"go"}}}}
	}
	item := current[2]
	item.Item.Source.ID = 1
	current[2] = item
	item = current[3]
	item.Item.Enhancement = nil
	current[3] = item
	profile := Profile{Excluded: map[int64]string{4: NotInterestedArticle}, Keywords: map[string]Evidence{"go": {Weight: 6}}}
	ordered := RankV1(candidates, current, profile, now)
	if len(ordered) != 3 || ordered[0].ID != 2 || ordered[1].ID != 3 || ordered[2].ID != 1 {
		t.Fatalf("偏好/排除/同分排序错误: %+v", ordered)
	}
	if ordered[0].Reason != "keyword_match" || ordered[1].Reason != "recent" {
		t.Fatalf("原因错误: %+v", ordered)
	}
	// 同来源候选接连占优时，来源惩罚应让其它来源候选提前。
	item = current[3]
	item.Item.Enhancement = &articleDomain.Enhancement{Keywords: []string{"go"}}
	current[3] = item
	item = current[2]
	item.Item.Source.ID = 1
	current[2] = item
	profile.Keywords["go"] = Evidence{Weight: 20}
	ordered = RankV1(candidates, current, profile, now)
	if ordered[0].ID != 3 || ordered[1].SourceID != 1 {
		t.Fatalf("来源打散顺序错误: %+v", ordered)
	}
}

func TestRankV1BoundsMissingMetadataAndTieBreak(t *testing.T) {
	now := time.Now().UTC()
	candidates := []articlesearch.RankedCandidate{{Candidate: articlesearch.Candidate{ArticleID: 1}}, {Candidate: articlesearch.Candidate{ArticleID: 2}}}
	current := map[int64]articlesearch.CurrentArticle{
		1: {Item: articleDomain.ListItem{ID: 1, SortAt: now, Enhancement: &articleDomain.Enhancement{Topics: []string{"x", "x"}}}},
		2: {Item: articleDomain.ListItem{ID: 2, SortAt: now}},
	}
	profile := Profile{Topics: map[string]Evidence{"x": {Weight: 100}}}
	a := RankV1(candidates, current, profile, now)
	profile.Topics["x"] = Evidence{Weight: 20}
	b := RankV1(candidates, current, profile, now)
	if len(a) != 2 || a[0].ID != 1 || a[0].Score != b[0].Score {
		t.Fatalf("权重未限幅或无 AI/Source 候选被丢弃: %+v %+v", a, b)
	}
	delete(profile.Topics, "x")
	a = RankV1(candidates, current, profile, now)
	if a[0].ID != 2 {
		t.Fatalf("同分 ID 倒序错误: %+v", a)
	}
}

func TestRankV1PositiveAndNegativeEvidenceStayBoundedSeparately(t *testing.T) {
	now := time.Now().UTC()
	candidates := []articlesearch.RankedCandidate{{Candidate: articlesearch.Candidate{ArticleID: 1}, RRFScore: 1.0 / 61}}
	current := map[int64]articlesearch.CurrentArticle{1: {Item: articleDomain.ListItem{ID: 1, SortAt: now, Enhancement: &articleDomain.Enhancement{Keywords: []string{"go"}}}}}
	profile := Profile{Keywords: map[string]Evidence{"go": {Weight: 0, PositiveWeight: 20, NegativeWeight: 20}}}
	item := RankV1(candidates, current, profile, now)[0]
	if item.Reason != "keyword_match" {
		t.Fatalf("正向证据被净权重隐藏: %+v", item)
	}
	profile.Keywords["go"] = Evidence{Weight: 0, PositiveWeight: 100, NegativeWeight: 100}
	boundedItem := RankV1(candidates, current, profile, now)[0]
	if item.Score != boundedItem.Score {
		t.Fatalf("正负权重未分别限幅: %+v %+v", item, boundedItem)
	}
}
