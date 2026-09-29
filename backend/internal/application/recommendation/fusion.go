package recommendation

import "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"

// FuseCurrent 先按 PostgreSQL 当前公开事实及向量身份过滤，再对有效序列重新编号。
func FuseCurrent(bm25, knn []articlesearch.Candidate, current map[int64]articlesearch.CurrentArticle, profile string) []articlesearch.RankedCandidate {
	validBM := make([]articlesearch.Candidate, 0, len(bm25))
	for _, candidate := range bm25 {
		if _, ok := current[candidate.ArticleID]; ok {
			validBM = append(validBM, candidate)
		}
	}
	validKNN := make([]articlesearch.Candidate, 0, len(knn))
	for _, candidate := range knn {
		item, ok := current[candidate.ArticleID]
		if ok && candidate.Identity.Profile == profile && candidate.Identity.Matches(item.Identity) {
			validKNN = append(validKNN, candidate)
		}
	}
	return articlesearch.FuseRRF(validBM, validKNN)
}
