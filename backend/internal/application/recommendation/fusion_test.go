package recommendation

import (
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
)

func TestFuseCurrentTwoPathsAndStaleVector(t *testing.T) {
	now := time.Now().UTC()
	identity := articlesearch.VectorIdentity{RevisionID: 1, GenerationID: "11111111-1111-1111-1111-111111111111", EmbeddingID: "22222222-2222-2222-2222-222222222222", Profile: "e-v2"}
	candidate := func(id int64) articlesearch.Candidate {
		return articlesearch.Candidate{ArticleID: id, Position: articlesearch.SortPosition{Score: 1, PublishedAt: now, ArticleID: id}}
	}
	bm := []articlesearch.Candidate{candidate(1), candidate(2)}
	knn := []articlesearch.Candidate{candidate(2), candidate(3), candidate(4)}
	for i := range knn {
		knn[i].Identity = identity
	}
	stale := identity
	stale.RevisionID++
	current := map[int64]articlesearch.CurrentArticle{
		1: {}, 2: {Identity: identity}, 3: {Identity: stale},
	}
	fused := FuseCurrent(bm, knn, current, "e-v2")
	if len(fused) != 2 || fused[0].ArticleID != 2 || fused[0].RRFScore <= fused[1].RRFScore || !fused[0].Semantic {
		t.Fatalf("融合/旧向量/去重错误: %+v", fused)
	}
	if one := FuseCurrent(bm, nil, current, "e-v2"); len(one) != 2 || one[0].Semantic {
		t.Fatalf("单路 BM25 失败: %+v", one)
	}
	if one := FuseCurrent(nil, knn, current, "e-v2"); len(one) != 1 || one[0].ArticleID != 2 {
		t.Fatalf("单路 KNN 失败: %+v", one)
	}
	if empty := FuseCurrent(nil, nil, current, "e-v2"); len(empty) != 0 {
		t.Fatalf("空召回应为空: %+v", empty)
	}
}
