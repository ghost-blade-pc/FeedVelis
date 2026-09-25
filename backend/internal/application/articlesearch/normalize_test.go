package articlesearch

import (
	"context"
	"strings"
	"testing"
	"time"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type fakeIndex struct {
	createCalls int
	searchCalls int
	closeCalls  []string
	createErr   error
	closeErr    error
	search      func(IndexRequest) (CandidateBatch, error)
}

func (f *fakeIndex) CreatePIT(context.Context, time.Duration) (string, error) {
	f.createCalls++
	return "pit-1", f.createErr
}
func (f *fakeIndex) Search(_ context.Context, request IndexRequest) (CandidateBatch, error) {
	f.searchCalls++
	return f.search(request)
}
func (f *fakeIndex) ClosePIT(_ context.Context, pitID string) error {
	f.closeCalls = append(f.closeCalls, pitID)
	return f.closeErr
}

type fakeReader struct {
	calls int
	err   error
	items map[int64]articleDomain.ListItem
}

func (f *fakeReader) ListPublishedByIDs(_ context.Context, ids []int64) ([]articleDomain.ListItem, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	result := make([]articleDomain.ListItem, 0, len(ids))
	for index := len(ids) - 1; index >= 0; index-- { // 故意模拟数据库乱序
		if item, ok := f.items[ids[index]]; ok {
			result = append(result, item)
		}
	}
	return result, nil
}

func TestInvalidRequestsStopBeforeQueryIndex(t *testing.T) {
	tooLongQuery := strings.Repeat("界", 201)
	tooLongFilter := strings.Repeat("标", 65)
	empty := "  \r\n "
	zero, negative := int64(0), int64(-1)
	cases := []Request{
		{}, {Q: empty}, {Q: tooLongQuery}, {Q: "ok", Keyword: &empty}, {Q: "ok", Topic: &tooLongFilter},
		{Q: "ok", SourceID: &zero}, {Q: "ok", SourceID: &negative}, {Q: "ok", Limit: -1}, {Q: "ok", Limit: 51},
	}
	for index, request := range cases {
		queryIndex := &fakeIndex{search: func(IndexRequest) (CandidateBatch, error) { return CandidateBatch{}, nil }}
		service := testService(t, queryIndex, &fakeReader{}, Config{PITKeepAlive: 2 * time.Minute, CandidateBatchSize: 100, MaxCandidatesPerRequest: 500})
		_, err := service.Search(context.Background(), request)
		if CodeOf(err) != CodeValidationFailed {
			t.Fatalf("case %d: error = %v", index, err)
		}
		if queryIndex.createCalls != 0 || queryIndex.searchCalls != 0 {
			t.Fatalf("case %d: 非法请求触发 QueryIndex: %+v", index, queryIndex)
		}
	}
}

func TestNormalizeRepairsUTF8AndCountsUnicodeRunes(t *testing.T) {
	keyword, topic, source := "  关键词\r\n ", "topic\x00name", int64(8)
	query, err := Normalize(Request{Q: "  Go\xff 搜索\r\n", Keyword: &keyword, Topic: &topic, SourceID: &source})
	if err != nil {
		t.Fatal(err)
	}
	if query.Q != "Go� 搜索" || query.Filters.Keyword != "关键词" || query.Filters.Topic != "topic�name" || query.Filters.SourceID != 8 || query.Limit != 20 {
		t.Fatalf("规范化结果不符: %+v", query)
	}
}

func testService(t *testing.T, index QueryIndex, reader PublicArticleReader, config Config) *Service {
	t.Helper()
	codec, err := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(index, reader, codec, nil, config)
	service.now = func() time.Time { return time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC) }
	return service
}
