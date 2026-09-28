package articlesearch

import "sort"

type RankedCandidate struct {
	Candidate
	Semantic bool
	RRFScore float64
}

// FuseRRF 保留通道固定顺序，按文章去重后重新编号，最后以时间和 ID 打破同分。
func FuseRRF(bm25, knn []Candidate) []RankedCandidate {
	byID := make(map[int64]*RankedCandidate, len(bm25)+len(knn))
	for channel, list := range [][]Candidate{bm25, knn} {
		seen := map[int64]bool{}
		rank := 0
		for _, c := range list {
			if c.ArticleID <= 0 || seen[c.ArticleID] {
				continue
			}
			seen[c.ArticleID] = true
			rank++
			item := byID[c.ArticleID]
			if item == nil {
				item = &RankedCandidate{Candidate: c}
				byID[c.ArticleID] = item
			}
			item.RRFScore += 1 / float64(RRFConstant+rank)
			if channel == 1 {
				item.Semantic = true
				item.Identity = c.Identity
			}
		}
	}
	result := make([]RankedCandidate, 0, len(byID))
	for _, item := range byID {
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.RRFScore != b.RRFScore {
			return a.RRFScore > b.RRFScore
		}
		if !a.Position.PublishedAt.Equal(b.Position.PublishedAt) {
			return a.Position.PublishedAt.After(b.Position.PublishedAt)
		}
		return a.ArticleID > b.ArticleID
	})
	return result
}
