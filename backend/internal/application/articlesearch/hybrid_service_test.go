package articlesearch

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type hybridIndex struct {
	bm, knn           []Candidate
	bmErr, knnErr     error
	schema            bool
	knnWait           bool
	bmCalls, knnCalls atomic.Int32
	closed            atomic.Int32
}

func (i *hybridIndex) CreatePIT(context.Context, time.Duration) (string, error) { return "pit", nil }
func (i *hybridIndex) ClosePIT(ctx context.Context, _ string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	i.closed.Add(1)
	return nil
}
func (i *hybridIndex) Search(ctx context.Context, _ IndexRequest) (CandidateBatch, error) {
	i.bmCalls.Add(1)
	return CandidateBatch{PITID: "pit", Candidates: i.bm, Exhausted: true}, i.bmErr
}
func (i *hybridIndex) SearchKNN(ctx context.Context, _ KNNRequest) (CandidateBatch, error) {
	i.knnCalls.Add(1)
	if i.knnWait {
		<-ctx.Done()
		return CandidateBatch{}, ctx.Err()
	}
	return CandidateBatch{PITID: "pit", Candidates: i.knn, Exhausted: true}, i.knnErr
}
func (i *hybridIndex) SupportsSemantic(context.Context, int) (bool, error) { return i.schema, nil }

type queryEmbeddingFake struct {
	calls  atomic.Int32
	vector []float64
	err    error
	wait   bool
}

func (e *queryEmbeddingFake) EmbedQuery(ctx context.Context, _ string) ([]float64, error) {
	e.calls.Add(1)
	if e.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return e.vector, e.err
}

type currentFake struct {
	items map[int64]CurrentArticle
	err   error
	calls int
}

func (r *currentFake) ListPublishedByIDs(context.Context, []int64) ([]articleDomain.ListItem, error) {
	return nil, r.err
}
func (r *currentFake) ListPublishedWithIdentity(_ context.Context, ids []int64) ([]CurrentArticle, error) {
	r.calls++
	result := []CurrentArticle{}
	for j := len(ids) - 1; j >= 0; j-- {
		if item, ok := r.items[ids[j]]; ok {
			result = append(result, item)
		}
	}
	return result, r.err
}

func hybridFixture(t *testing.T) (*Service, *hybridIndex, *queryEmbeddingFake, *currentFake) {
	t.Helper()
	now := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	identity := VectorIdentity{RevisionID: 10, GenerationID: "97000000-0000-0000-0000-000000000001", EmbeddingID: "97000000-0000-0000-0000-000000000003", Profile: "e-v1"}
	c := func(id int64) Candidate {
		return Candidate{ArticleID: id, Position: SortPosition{ArticleID: id, Score: 1, PublishedAt: now}, Identity: identity}
	}
	index := &hybridIndex{bm: []Candidate{c(1), c(2)}, knn: []Candidate{c(2), c(3)}, schema: true}
	embed := &queryEmbeddingFake{vector: []float64{1, 0}}
	reader := &currentFake{items: map[int64]CurrentArticle{}}
	for id := int64(1); id <= 3; id++ {
		reader.items[id] = CurrentArticle{Item: articleDomain.ListItem{ID: id, Title: "当前公开内容"}, Identity: identity}
	}
	codec, _ := NewCursorCodec([]byte(strings.Repeat("k", 32)))
	s := NewService(index, reader, codec, nil, Config{PITKeepAlive: time.Minute, Timeout: time.Second, CandidateBatchSize: 100, MaxCandidatesPerRequest: 500, Hybrid: HybridConfig{Enabled: true, BM25Candidates: 100, KNNCandidates: 100, EmbeddingTimeout: 20 * time.Millisecond, KNNTimeout: 20 * time.Millisecond, Profile: "e-v1", Dimensions: 2}}).WithHybrid(embed, reader)
	s.now = func() time.Time { return now }
	return s, index, embed, reader
}

func TestHybridFrozenPaginationNeverRecalls(t *testing.T) {
	s, index, embed, reader := hybridFixture(t)
	first, err := s.Search(context.Background(), Request{Q: "go", Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != 2 || !first.HasMore {
		t.Fatalf("第一页: %+v %v", first, err)
	}
	query, _ := Normalize(Request{Q: "go"})
	state, err := s.codec.DecodeFrozen(query, s.config.Hybrid, *first.NextCursor, s.now())
	if err != nil {
		t.Fatal(err)
	}
	index.bm = nil
	index.knn = nil
	index.knnErr = errors.New("断开")
	embed.err = errors.New("模型波动")
	second, err := s.Search(context.Background(), Request{Q: "go", Limit: 1, Cursor: *first.NextCursor})
	if err != nil || second.Items[0].ID != 1 || !second.HasMore {
		t.Fatalf("第二页: %+v %v", second, err)
	}
	state2, err := s.codec.DecodeFrozen(query, s.config.Hybrid, *second.NextCursor, s.now())
	if err != nil || !state2.ExpiresAt.Equal(state.ExpiresAt) {
		t.Fatal("续页 TTL 改变")
	}
	last, err := s.Search(context.Background(), Request{Q: "go", Limit: 50, Cursor: *second.NextCursor})
	if err != nil || len(last.Items) != 1 || last.Items[0].ID != 3 || last.HasMore {
		t.Fatalf("尾页: %+v %v", last, err)
	}
	if embed.calls.Load() != 1 || index.knnCalls.Load() != 1 || index.bmCalls.Load() != 1 || index.closed.Load() != 1 || reader.calls != 3 {
		t.Fatalf("续页重新召回: embed=%d knn=%d bm=%d closed=%d reads=%d", embed.calls.Load(), index.knnCalls.Load(), index.bmCalls.Load(), index.closed.Load(), reader.calls)
	}
}

func TestHybridFallbacksAndRequiredFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Service, *hybridIndex, *queryEmbeddingFake, *currentFake)
		code   ErrorCode
	}{
		{"模型未配置", func(s *Service, _ *hybridIndex, _ *queryEmbeddingFake, _ *currentFake) { s.embedder = nil }, ""},
		{"v1 schema", func(_ *Service, i *hybridIndex, _ *queryEmbeddingFake, _ *currentFake) { i.schema = false }, ""},
		{"模型失败", func(_ *Service, _ *hybridIndex, e *queryEmbeddingFake, _ *currentFake) { e.err = errors.New("失败") }, ""},
		{"模型超时", func(_ *Service, _ *hybridIndex, e *queryEmbeddingFake, _ *currentFake) { e.wait = true }, ""},
		{"无效向量", func(_ *Service, _ *hybridIndex, e *queryEmbeddingFake, _ *currentFake) { e.vector = []float64{0, 0} }, ""},
		{"KNN失败", func(_ *Service, i *hybridIndex, _ *queryEmbeddingFake, _ *currentFake) {
			i.knnErr = errors.New("失败")
		}, ""},
		{"KNN超时", func(_ *Service, i *hybridIndex, _ *queryEmbeddingFake, _ *currentFake) { i.knnWait = true }, ""},
		{"KNN为空", func(_ *Service, i *hybridIndex, _ *queryEmbeddingFake, _ *currentFake) { i.knn = nil }, ""},
		{"BM25失败", func(_ *Service, i *hybridIndex, _ *queryEmbeddingFake, _ *currentFake) {
			i.bmErr = errors.New("失败")
		}, CodeSearchUnavailable},
		{"数据库失败", func(_ *Service, _ *hybridIndex, _ *queryEmbeddingFake, r *currentFake) { r.err = errors.New("失败") }, CodeDependencyUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, i, e, r := hybridFixture(t)
			tc.mutate(s, i, e, r)
			p, err := s.Search(context.Background(), Request{Q: "go", Limit: 1})
			if tc.code != "" {
				if CodeOf(err) != tc.code || len(p.Items) != 0 {
					t.Fatalf("必需路径返回部分页: %+v %v", p, err)
				}
				return
			}
			if err != nil || p.Items[0].ID != 1 || !p.HasMore {
				t.Fatalf("降级失败: %+v %v", p, err)
			}
			query, _ := Normalize(Request{Q: "go"})
			state, err := s.codec.DecodeFrozen(query, s.config.Hybrid, *p.NextCursor, s.now())
			if err != nil || state.Mode != ModeBM25 {
				t.Fatalf("降级未冻结: %+v %v", state, err)
			}
			before := e.calls.Load()
			e.err = nil
			e.wait = false
			i.schema = true
			i.knnErr = nil
			page, err := s.Search(context.Background(), Request{Q: "go", Cursor: *p.NextCursor})
			if err != nil || len(page.Items) != 1 || page.Items[0].ID != 2 || e.calls.Load() != before {
				t.Fatalf("降级续页恢复语义: %+v %v", page, err)
			}
		})
	}
}

func TestHybridRemovesStaleContributionsAndRevalidatesPages(t *testing.T) {
	for _, mutate := range []func(*VectorIdentity){func(v *VectorIdentity) { v.RevisionID++ }, func(v *VectorIdentity) { v.GenerationID = "97000000-0000-0000-0000-000000000004" }, func(v *VectorIdentity) { v.EmbeddingID = "97000000-0000-0000-0000-000000000005" }, func(v *VectorIdentity) { v.Profile = "old-profile" }} {
		s, i, _, r := hybridFixture(t)
		for id := int64(2); id <= 3; id++ {
			item := r.items[id]
			mutate(&item.Identity)
			r.items[id] = item
		}
		page, err := s.Search(context.Background(), Request{Q: "go", Limit: 50})
		if err != nil || len(page.Items) != 2 || page.Items[0].ID != 1 || page.Items[1].ID != 2 {
			t.Fatalf("旧身份仍贡献: %+v %v", page, err)
		}
		if i.knnCalls.Load() != 1 {
			t.Fatal("未测试 KNN")
		}
	}
	s, _, _, r := hybridFixture(t)
	first, err := s.Search(context.Background(), Request{Q: "go", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	// 冻结后的纯文本候选允许新公开修订；语义候选身份变化整篇跳过。
	text := r.items[1]
	text.Identity.RevisionID++
	text.Item.Title = "新公开修订"
	r.items[1] = text
	semantic := r.items[3]
	semantic.Identity.RevisionID++
	r.items[3] = semantic
	last, err := s.Search(context.Background(), Request{Q: "go", Cursor: *first.NextCursor})
	if err != nil || len(last.Items) != 1 || last.Items[0].Title != "新公开修订" || last.HasMore {
		t.Fatalf("续页身份复核失败: %+v %v", last, err)
	}
}

func TestHybridVisibilityGapsAndCursorValidation(t *testing.T) {
	s, i, e, r := hybridFixture(t)
	first, err := s.Search(context.Background(), Request{Q: "go", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	delete(r.items, 1)
	delete(r.items, 3)
	empty, err := s.Search(context.Background(), Request{Q: "go", Cursor: *first.NextCursor})
	if err != nil || len(empty.Items) != 0 || empty.HasMore {
		t.Fatalf("耗尽: %+v %v", empty, err)
	}
	before := e.calls.Load()
	for _, request := range []Request{{Q: "", Cursor: ""}, {Q: "other", Cursor: *first.NextCursor}, {Q: "go", Cursor: "3.invalid"}} {
		if _, err = s.Search(context.Background(), request); err == nil {
			t.Fatal("非法请求通过")
		}
	}
	if e.calls.Load() != before || i.knnCalls.Load() != 1 {
		t.Fatal("非法请求调用模型")
	}
	s.config.Hybrid.Enabled = false
	if _, err = s.Search(context.Background(), Request{Q: "go", Cursor: *first.NextCursor}); CodeOf(err) != CodeInvalidCursor {
		t.Fatal("关闭开关旧游标有效")
	}
}

func TestHybridLegacyV1CursorAndPlanChange(t *testing.T) {
	s, i, e, _ := hybridFixture(t)
	query, _ := Normalize(Request{Q: "go"})
	legacy, err := s.codec.Encode(query, "pit", i.bm[0].Position, s.now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Search(context.Background(), Request{Q: "go", Cursor: legacy})
	if err != nil {
		t.Fatal(err)
	}
	if e.calls.Load() != 0 || i.knnCalls.Load() != 0 {
		t.Fatal("v1 续页调用模型")
	}
	first, err := s.Search(context.Background(), Request{Q: "go", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	s.config.Hybrid.KNNCandidates = 99
	if _, err = s.Search(context.Background(), Request{Q: "go", Cursor: *first.NextCursor}); CodeOf(err) != CodeInvalidCursor {
		t.Fatal("计划变更游标有效")
	}
}

func TestHybridSkipsChangedOverlapAndEmptySnapshot(t *testing.T) {
	s, i, _, r := hybridFixture(t)
	i.bm = append(i.bm, i.knn[1])
	i.knn = append([]Candidate{i.bm[0]}, i.knn...)
	first, err := s.Search(context.Background(), Request{Q: "go", Limit: 1})
	if err != nil || first.Items[0].ID != 1 {
		t.Fatalf("第一页: %+v %v", first, err)
	}
	overlap := r.items[2]
	overlap.Identity.EmbeddingID = "97000000-0000-0000-0000-000000000099"
	r.items[2] = overlap
	next, err := s.Search(context.Background(), Request{Q: "go", Cursor: *first.NextCursor})
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != 3 {
		t.Fatalf("陈旧重叠未整篇跳过: %+v %v", next, err)
	}
	r.items = map[int64]CurrentArticle{}
	empty, err := s.Search(context.Background(), Request{Q: "go"})
	if err != nil || len(empty.Items) != 0 || empty.HasMore {
		t.Fatalf("零有效候选: %+v %v", empty, err)
	}
}

func TestHybridTotalDeadlineNeverReturnsDegradedPartialPage(t *testing.T) {
	s, _, embed, reader := hybridFixture(t)
	s.config.Timeout = time.Millisecond
	embed.wait = true
	page, err := s.Search(context.Background(), Request{Q: "go"})
	if CodeOf(err) != CodeSearchUnavailable || len(page.Items) != 0 || reader.calls != 0 {
		t.Fatalf("总超时伪装成降级: %+v %v", page, err)
	}
}
