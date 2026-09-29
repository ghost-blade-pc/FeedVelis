package recommendation

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type recommendProfileFake struct {
	profile  Profile
	excluded map[int64]string
	err      error
	reads    int
}

func (f *recommendProfileFake) Profile(context.Context, string, []int64) (Profile, error) {
	f.reads++
	return f.profile, f.err
}
func (f *recommendProfileFake) Exclusions(_ context.Context, _ string, ids []int64) (map[int64]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	result := map[int64]string{}
	for _, id := range ids {
		if f.excluded[id] != "" {
			result[id] = f.excluded[id]
		}
	}
	return result, nil
}

type recommendIndexFake struct {
	candidates []articlesearch.Candidate
	err        error
	calls      int
}

type recommendHybridFake struct {
	recommendIndexFake
	result      HybridRecall
	hybridError error
	hybridCalls int
}

func (f *recommendHybridFake) RecallHybrid(context.Context, Terms, int, int, time.Duration, []float64, string, time.Duration) (HybridRecall, error) {
	f.hybridCalls++
	return f.result, f.hybridError
}

type recommendEmbedFake struct {
	vector []float64
	err    error
	calls  int
}

func (f *recommendEmbedFake) EmbedQuery(context.Context, string) ([]float64, error) {
	f.calls++
	return f.vector, f.err
}

func (f *recommendIndexFake) Recall(context.Context, Terms, int, time.Duration) ([]articlesearch.Candidate, error) {
	f.calls++
	return f.candidates, f.err
}

type recommendReaderFake struct {
	items    map[int64]articlesearch.CurrentArticle
	excluded map[int64]bool
	err      error
	now      time.Time
}

func (f *recommendReaderFake) ListPublishedWithIdentity(_ context.Context, ids []int64) ([]articlesearch.CurrentArticle, error) {
	if f.err != nil {
		return nil, f.err
	}
	result := []articlesearch.CurrentArticle{}
	for _, id := range ids {
		if item, ok := f.items[id]; ok {
			result = append(result, item)
		}
	}
	return result, nil
}
func (f *recommendReaderFake) ListRecommendationLatest(_ context.Context, cursor *articleDomain.Cursor, limit int, skip []int64, userID string, _, startedAt time.Time) ([]articleDomain.ListItem, error) {
	if f.err != nil {
		return nil, f.err
	}
	ignored := map[int64]bool{}
	for _, id := range skip {
		ignored[id] = true
	}
	result := []articleDomain.ListItem{}
	for id, item := range f.items {
		if ignored[id] || userID != "" && f.excluded[id] || item.Item.SortAt.After(startedAt) || cursor != nil && (item.Item.SortAt.After(cursor.SortAt) || item.Item.SortAt.Equal(cursor.SortAt) && id >= cursor.ArticleID) {
			continue
		}
		result = append(result, item.Item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SortAt.Equal(result[j].SortAt) {
			return result[i].ID > result[j].ID
		}
		return result[i].SortAt.After(result[j].SortAt)
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func recommendFixture(t *testing.T) (*Service, *recommendProfileFake, *recommendIndexFake, *recommendReaderFake) {
	t.Helper()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	reader := &recommendReaderFake{items: map[int64]articlesearch.CurrentArticle{}, excluded: map[int64]bool{}, now: now}
	for id := int64(1); id <= 5; id++ {
		reader.items[id] = articlesearch.CurrentArticle{Item: articleDomain.ListItem{ID: id, SortAt: now.Add(-time.Duration(id) * time.Hour), Enhancement: &articleDomain.Enhancement{Keywords: []string{"go"}}}}
	}
	profile := &recommendProfileFake{profile: Profile{Keywords: map[string]Evidence{"go": {Weight: 3}}}, excluded: map[int64]string{}}
	index := &recommendIndexFake{candidates: []articlesearch.Candidate{{ArticleID: 3}, {ArticleID: 1}}}
	codec, err := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(profile, index, reader, codec, nil, Config{FirstQueryTimeout: time.Second, BM25Candidates: 100, KNNCandidates: 100, CursorTTL: 2 * time.Minute}, func() time.Time { return now })
	return service, profile, index, reader
}

func TestRecommendFrozenOrderVisibilityFeedbackAndLatestFill(t *testing.T) {
	service, profile, index, reader := recommendFixture(t)
	first, err := service.Get(context.Background(), "72000000-0000-0000-0000-000000000001", 1, "")
	if err != nil || len(first.Items) != 1 || first.Items[0].Article.ID != 3 || first.Mode != "personalized" || first.NextCursor == nil {
		t.Fatalf("首查错误: %+v %v", first, err)
	}
	delete(reader.items, 1)
	reader.excluded[2] = true
	profile.excluded[1] = NotInterestedArticle
	second, err := service.Get(context.Background(), "72000000-0000-0000-0000-000000000001", 2, *first.NextCursor)
	if err != nil || len(second.Items) != 2 || second.Items[0].Article.ID != 4 || second.Items[1].Article.ID != 5 || !second.Degraded || second.Mode != "personalized" {
		t.Fatalf("续页过滤/补位错误: %+v %v", second, err)
	}
	if index.calls != 1 || profile.reads != 1 {
		t.Fatalf("续页重新召回或读取偏好: index=%d profile=%d", index.calls, profile.reads)
	}
}

func TestRecommendColdStartAndSearchFailure(t *testing.T) {
	service, profile, index, reader := recommendFixture(t)
	profile.profile = Profile{}
	page, err := service.Get(context.Background(), "", 2, "")
	if err != nil || page.Mode != "cold_start" || page.Degraded || page.Items[0].Article.ID != 1 || page.Items[0].Reason != "recent" || index.calls != 0 || profile.reads != 0 {
		t.Fatalf("匿名冷启动错误: %+v %v", page, err)
	}
	profile.profile = Profile{Keywords: map[string]Evidence{"go": {Weight: 1}}}
	index.err = errors.New("OpenSearch 故障")
	page, err = service.Get(context.Background(), "72000000-0000-0000-0000-000000000001", 2, "")
	if err != nil || page.Mode != "latest_fallback" || !page.Degraded || page.Items[0].Reason != "latest_fallback" || page.NextCursor == nil {
		t.Fatalf("搜索故障未回退: %+v %v", page, err)
	}
	reader.err = errors.New("PostgreSQL 故障")
	if _, err := service.Get(context.Background(), "", 1, ""); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("PostgreSQL 故障被吞掉: %v", err)
	}
	reader.err = nil
	profile.err = errors.New("画像故障")
	if _, err := service.Get(context.Background(), "72000000-0000-0000-0000-000000000001", 1, ""); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("画像故障被吞掉: %v", err)
	}
}

func TestRecommendSemanticFailureKeepsBM25AndDoesNotReembedOnPage(t *testing.T) {
	service, _, _, _ := recommendFixture(t)
	hybrid := &recommendHybridFake{result: HybridRecall{BM25: []articlesearch.Candidate{{ArticleID: 3}}, SemanticReason: "knn_failed"}}
	embed := &recommendEmbedFake{vector: []float64{1, 0}}
	service.index, service.embedder = hybrid, embed
	service.config.SemanticEnabled = true
	service.config.EmbeddingProfile = "e-v2"
	service.config.Dimensions = 2
	service.config.EmbeddingTimeout = time.Second
	service.config.KNNTimeout = time.Second
	first, err := service.Get(context.Background(), "72000000-0000-0000-0000-000000000001", 1, "")
	if err != nil || !first.Degraded || first.Mode != "personalized" || first.Items[0].Article.ID != 3 || hybrid.hybridCalls != 1 || embed.calls != 1 {
		t.Fatalf("语义故障未保留 BM25: %+v %v", first, err)
	}
	if _, err := service.Get(context.Background(), "72000000-0000-0000-0000-000000000001", 1, *first.NextCursor); err != nil || hybrid.hybridCalls != 1 || embed.calls != 1 {
		t.Fatalf("续页重复召回/Embedding: %v", err)
	}
}
