package articlesearch

import (
	"context"
	"errors"
	"testing"
	"time"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

func TestServiceKeepsHitOrderAndLookaheadCursor(t *testing.T) {
	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	positions := map[int64]SortPosition{}
	for id := int64(1); id <= 3; id++ {
		positions[id] = SortPosition{Score: float64(4 - id), PublishedAt: base.Add(-time.Duration(id) * time.Minute), ArticleID: id}
	}
	index := &fakeIndex{}
	index.search = func(request IndexRequest) (CandidateBatch, error) {
		if request.After == nil {
			return CandidateBatch{PITID: "pit-new", Candidates: []Candidate{{1, positions[1]}, {2, positions[2]}, {3, positions[3]}}, Exhausted: true}, nil
		}
		if request.After.ArticleID == 2 {
			return CandidateBatch{PITID: "pit-final", Candidates: []Candidate{{3, positions[3]}}, Exhausted: true}, nil
		}
		t.Fatalf("意外 search_after: %+v", request.After)
		return CandidateBatch{}, nil
	}
	reader := &fakeReader{items: map[int64]articleDomain.ListItem{
		1: {ID: 1, Title: "current-one"}, 2: {ID: 2, Title: "current-two"}, 3: {ID: 3, Title: "current-three"},
	}}
	service := testService(t, index, reader, Config{PITKeepAlive: 2 * time.Minute, CandidateBatchSize: 100, MaxCandidatesPerRequest: 500})
	first, err := service.Search(context.Background(), Request{Q: "go", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.Items[0].ID != 1 || first.Items[1].ID != 2 || !first.HasMore || first.NextCursor == nil || len(index.closeCalls) != 0 {
		t.Fatalf("第一页错误: %+v close=%v", first, index.closeCalls)
	}
	second, err := service.Search(context.Background(), Request{Q: "go", Limit: 2, Cursor: *first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != 3 || second.HasMore || len(index.closeCalls) != 1 || index.closeCalls[0] != "pit-final" {
		t.Fatalf("第二页错误: %+v close=%v", second, index.closeCalls)
	}
}

func TestServiceScansInBatchesAndAdvancesShortPageAtLimit(t *testing.T) {
	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	position := func(id int64) SortPosition { return SortPosition{Score: 1, PublishedAt: base, ArticleID: id} }
	index := &fakeIndex{}
	index.search = func(request IndexRequest) (CandidateBatch, error) {
		if request.After == nil {
			return CandidateBatch{PITID: "pit-2", Candidates: []Candidate{{1, position(1)}, {2, position(2)}}}, nil
		}
		return CandidateBatch{PITID: "pit-3", Candidates: []Candidate{{3, position(3)}, {4, position(4)}}}, nil
	}
	reader := &fakeReader{items: map[int64]articleDomain.ListItem{4: {ID: 4, Title: "current revision"}}}
	service := testService(t, index, reader, Config{PITKeepAlive: 2 * time.Minute, CandidateBatchSize: 2, MaxCandidatesPerRequest: 4})
	page, err := service.Search(context.Background(), Request{Q: "go", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != 4 || !page.HasMore || page.NextCursor == nil || reader.calls != 2 {
		t.Fatalf("扫描上限页面错误: %+v reader_calls=%d", page, reader.calls)
	}
	_, after, err := service.codec.Decode(Query{Q: "go", Limit: 3}, *page.NextCursor, service.now())
	if err != nil || after.ArticleID != 4 {
		t.Fatalf("cursor 未越过最后检查候选: %+v err=%v", after, err)
	}
}

func TestServiceFailuresReturnNoPartialPageAndClosePIT(t *testing.T) {
	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	index := &fakeIndex{search: func(IndexRequest) (CandidateBatch, error) {
		return CandidateBatch{PITID: "pit-latest", Candidates: []Candidate{{1, SortPosition{Score: 1, PublishedAt: base, ArticleID: 1}}}}, nil
	}, closeErr: errors.New("close failed")}
	reader := &fakeReader{err: errors.New("database down")}
	service := testService(t, index, reader, Config{PITKeepAlive: time.Minute, CandidateBatchSize: 10, MaxCandidatesPerRequest: 20})
	page, err := service.Search(context.Background(), Request{Q: "go"})
	if CodeOf(err) != CodeDependencyUnavailable || len(page.Items) != 0 || len(index.closeCalls) != 1 || index.closeCalls[0] != "pit-latest" {
		t.Fatalf("依赖失败分类/清理错误: page=%+v err=%v close=%v", page, err, index.closeCalls)
	}
}

func TestServiceMapsPITNotFoundToInvalidCursor(t *testing.T) {
	index := &fakeIndex{search: func(IndexRequest) (CandidateBatch, error) { return CandidateBatch{}, ErrPITNotFound }}
	service := testService(t, index, &fakeReader{}, Config{PITKeepAlive: time.Minute, CandidateBatchSize: 10, MaxCandidatesPerRequest: 20})
	query, _ := Normalize(Request{Q: "go"})
	cursor, _ := service.codec.Encode(query, "expired-pit", SortPosition{Score: 1, PublishedAt: service.now(), ArticleID: 1}, service.now().Add(time.Minute))
	page, err := service.Search(context.Background(), Request{Q: "go", Cursor: cursor})
	if CodeOf(err) != CodeInvalidCursor || len(page.Items) != 0 || len(index.closeCalls) != 1 {
		t.Fatalf("PIT not found 映射错误: page=%+v err=%v close=%v", page, err, index.closeCalls)
	}
}

func TestServiceRetriesSuccessfullyAfterIndexRecoveryInSameProcess(t *testing.T) {
	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	attempt := 0
	index := &fakeIndex{search: func(IndexRequest) (CandidateBatch, error) {
		attempt++
		if attempt == 1 {
			return CandidateBatch{}, errors.New("opensearch down")
		}
		return CandidateBatch{PITID: "pit-recovered", Candidates: []Candidate{{1, SortPosition{Score: 1, PublishedAt: base, ArticleID: 1}}}, Exhausted: true}, nil
	}}
	reader := &fakeReader{items: map[int64]articleDomain.ListItem{1: {ID: 1, Title: "恢复结果"}}}
	service := testService(t, index, reader, Config{PITKeepAlive: time.Minute, CandidateBatchSize: 10, MaxCandidatesPerRequest: 20})
	if _, err := service.Search(context.Background(), Request{Q: "go"}); CodeOf(err) != CodeSearchUnavailable {
		t.Fatalf("首次故障分类错误: %v", err)
	}
	page, err := service.Search(context.Background(), Request{Q: "go"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != 1 {
		t.Fatalf("同进程恢复后未重试成功: page=%+v err=%v", page, err)
	}
}
