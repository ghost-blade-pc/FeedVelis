package articlesearch

import (
	"math"
	"testing"
	"time"
)

func TestRRFExactScoresAndDedup(t *testing.T) {
	now := time.Now()
	c := func(id int64) Candidate {
		return Candidate{ArticleID: id, Position: SortPosition{ArticleID: id, PublishedAt: now}}
	}
	list := FuseRRF([]Candidate{c(1), c(1), c(2)}, []Candidate{c(2), c(3), c(3)})
	if len(list) != 3 || list[0].ArticleID != 2 || list[1].ArticleID != 1 || list[2].ArticleID != 3 {
		t.Fatalf("排序错误: %+v", list)
	}
	if math.Abs(list[0].RRFScore-(1.0/61+1.0/62)) > 1e-15 || list[1].RRFScore != 1.0/61 {
		t.Fatal("RRF 未按重新编号求和")
	}
	if len(FuseRRF(nil, nil)) != 0 {
		t.Fatal("空路失败")
	}
	single := FuseRRF(nil, []Candidate{c(1), c(2)})
	if single[0].ArticleID != 1 {
		t.Fatal("单路应保留次序")
	}
}

func TestRRFDeterministicTiesAndWindow(t *testing.T) {
	now := time.Now()
	bm, knn := []Candidate{}, []Candidate{}
	for i := int64(1); i <= 100; i++ {
		bm = append(bm, Candidate{ArticleID: i, Position: SortPosition{PublishedAt: now}})
		knn = append(knn, Candidate{ArticleID: i + 100, Position: SortPosition{PublishedAt: now}})
	}
	first := FuseRRF(bm, knn)
	if len(first) != 200 || first[0].ArticleID != 101 {
		t.Fatalf("200 条边界/ID 并列错误: %v", first[0])
	}
	for n := 0; n < 100; n++ {
		again := FuseRRF(bm, knn)
		for i := range first {
			if again[i].ArticleID != first[i].ArticleID {
				t.Fatal("次序不稳定")
			}
		}
	}
	knn[0].Position.PublishedAt = now.Add(-time.Second)
	if got := FuseRRF(bm, knn); got[0].ArticleID != 1 {
		t.Fatal("时间未打破同分")
	}
}
