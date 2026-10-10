package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

var trusted = Caller{UserID: "84000000-0000-0000-0000-000000000001"}

func ptr[T any](v T) *T { return &v }
func toolArticle(id int64) articleDomain.ListItem {
	return articleDomain.ListItem{ID: id, RevisionID: id + 100, Title: "标题", Origin: articleDomain.OriginUser, Author: &articleDomain.AuthorSummary{ID: trusted.UserID, Nickname: "作者"}, Excerpt: "摘录"}
}

type toolFixture struct {
	calls    int
	limit    int
	user     string
	maxChars int
	request  articlesearch.Request
	items    []articleDomain.ListItem
	content  string
	more     bool
	err      error
	block    bool
	duration time.Duration
}

func (f *toolFixture) wait(ctx context.Context) error {
	f.calls++
	if deadline, ok := ctx.Deadline(); ok {
		f.duration = time.Until(deadline)
	}
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return f.err
}
func (f *toolFixture) SearchOnce(ctx context.Context, r articlesearch.Request) (articlesearch.Page, error) {
	f.request = r
	f.limit = r.Limit
	return articlesearch.Page{Items: f.items, HasMore: f.more}, f.wait(ctx)
}
func (f *toolFixture) GetOnce(ctx context.Context, user string, limit int) (recommendation.Page, error) {
	f.user = user
	f.limit = limit
	page := recommendation.Page{Mode: "latest_fallback", Degraded: true, DegradeReason: "search_unavailable", HasMore: f.more}
	for _, item := range f.items {
		page.Items = append(page.Items, recommendation.Item{Article: item, Reason: "latest_fallback"})
	}
	return page, f.wait(ctx)
}
func (f *toolFixture) ReadPublicText(ctx context.Context, id int64, maxChars int) (articleApp.PublicText, error) {
	f.maxChars = maxChars
	content, cut := prefix(f.content, maxChars)
	return articleApp.PublicText{Item: toolArticle(id), Content: content, Truncated: cut}, f.wait(ctx)
}

func TestStrictInputsRejectBeforeDependencies(t *testing.T) {
	f := &toolFixture{}
	s := NewService(f, f, f, Config{})
	for _, raw := range []string{`{}`, `{"q":null}`, `{"q":""}`, `{"q":"\u0000"}`, `{"q":"\ud800"}`, `{"q":"x","q":"y"}`, `{"q":"x","user_id":"other"}`, `{"q":"x","conversation_id":"other"}`, `{"q":"x","cursor":"a"}`, `{"q":"x","limit":0}`, `{"q":"x","limit":11}`, `{"q":"x","limit":null}`, `{"q":"x","source_id":0}`, `{"q":"x","source_id":1.2}`, `{"q":"x","keyword":""}`, `{"q":"x","topic":null}`, `{"q":"` + strings.Repeat("界", 201) + `"}`, `{"q":"x","topic":"` + strings.Repeat("界", 65) + `"}`, "{\"q\":\"\xff\"}"} {
		if _, err := s.SearchJSON(context.Background(), trusted, []byte(raw)); CodeOf(err) != ValidationFailed {
			t.Errorf("非法搜索 %q: %v", raw, err)
		}
	}
	for _, raw := range []string{`{"limit":0}`, `{"limit":11}`, `{"q":"x"}`, `{"limit":null}`, `{"limit":1,"limit":2}`, `null`, `{"user_id":"other"}`} {
		if _, err := s.RecommendJSON(context.Background(), trusted, []byte(raw)); CodeOf(err) != ValidationFailed {
			t.Errorf("非法推荐 %q: %v", raw, err)
		}
	}
	for _, raw := range []string{`{}`, `{"article_id":null}`, `{"article_id":0}`, `{"article_id":-1}`, `{"article_id":"1"}`, `{"article_id":1.2}`, `{"article_id":9223372036854775808}`, `{"article_id":1,"max_chars":0}`, `{"article_id":1,"max_chars":8001}`, `{"article_id":1,"max_chars":null}`, `{"article_id":1,"url":"http://private"}`} {
		if _, err := s.GetJSON(context.Background(), trusted, []byte(raw)); CodeOf(err) != ValidationFailed {
			t.Errorf("非法正文 %q: %v", raw, err)
		}
	}
	if _, err := s.Search(context.Background(), Caller{}, SearchInput{Q: ptr("查询")}); CodeOf(err) != ValidationFailed {
		t.Fatal("空身份未拒绝")
	}
	if _, err := s.Search(context.Background(), trusted, SearchInput{Q: ptr("x\x00")}); CodeOf(err) != ValidationFailed {
		t.Fatal("类型入口非法字符未拒绝")
	}
	if f.calls != 0 {
		t.Fatal("非法输入访问依赖")
	}
}

func TestDefaultsBoundsIdentityAndStableErrors(t *testing.T) {
	f := &toolFixture{items: []articleDomain.ListItem{toolArticle(1)}, content: strings.Repeat("😀", 8001), more: true}
	s := NewService(f, f, f, Config{})
	search, err := s.SearchJSON(context.Background(), trusted, []byte(`{"q":" go ","source_id":2,"topic":"主题"}`))
	if err != nil || f.limit != 5 || len(search.Items) != 1 || !search.Truncated || search.Items[0].RevisionID != 101 || search.Items[0].SummarySource != "excerpt" {
		t.Fatalf("搜索默认: %+v %v", search, err)
	}
	if f.duration > 5*time.Second || f.duration < 4*time.Second {
		t.Fatal("默认deadline不是5秒")
	}
	recommended, err := s.RecommendJSON(context.Background(), trusted, []byte(`{"limit":10}`))
	if err != nil || f.user != trusted.UserID || f.limit != 10 || recommended.Mode != "latest_fallback" || !recommended.Degraded || recommended.DegradeReason == nil || recommended.Items[0].Reason != "latest_fallback" {
		t.Fatalf("本人推荐降级: %+v %v", recommended, err)
	}
	got, err := s.GetJSON(context.Background(), trusted, []byte(`{"article_id":1}`))
	if err != nil || f.maxChars != 4000 || len([]rune(got.Content)) != 4000 || !got.Truncated {
		t.Fatal("正文默认预算错误", err)
	}
	got, err = s.GetJSON(context.Background(), trusted, []byte(`{"article_id":1,"max_chars":8000}`))
	if err != nil || len([]rune(got.Content)) != 8000 {
		t.Fatal("正文边界错误", err)
	}
	f.err = &articlesearch.Error{Code: articlesearch.CodeSearchUnavailable, Cause: errors.New("敏感依赖文本")}
	if _, err := s.Search(context.Background(), trusted, SearchInput{Q: ptr("go")}); err == nil || err.Error() != "SEARCH_UNAVAILABLE" {
		t.Fatal("错误泄露依赖文本", err)
	}
	f.err = &articlesearch.Error{Code: articlesearch.CodeDependencyUnavailable, Cause: errors.New("敏感依赖文本")}
	if _, err := s.Search(context.Background(), trusted, SearchInput{Q: ptr("go")}); CodeOf(err) != DependencyUnavailable {
		t.Fatal("事实源错误分类", err)
	}
	f.err = articleDomain.ErrNotFound
	if _, err := s.Get(context.Background(), trusted, GetInput{ArticleID: ptr(int64(1))}); CodeOf(err) != ArticleNotFound {
		t.Fatal("非公开文章错误", err)
	}
	f.err = recommendation.ErrDependencyUnavailable
	if _, err := s.Recommend(context.Background(), trusted, RecommendInput{}); CodeOf(err) != DependencyUnavailable {
		t.Fatal("推荐事实源错误", err)
	}
	f.err = nil
	f.items = nil
	search, err = s.Search(context.Background(), trusted, SearchInput{Q: ptr("go")})
	if err != nil || search.Items == nil || len(search.Items) != 0 {
		t.Fatal("成功空结果应为[]", err)
	}
}

func TestDeadlineCancellationNoRetryAndShorterCaller(t *testing.T) {
	for _, tool := range []string{"search", "recommend", "get"} {
		for _, cancelFirst := range []bool{false, true} {
			f := &toolFixture{block: true}
			s := NewService(f, f, f, Config{Timeout: time.Second})
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
			if cancelFirst {
				cancel()
			}
			defer cancel()
			var err error
			switch tool {
			case "search":
				_, err = s.Search(ctx, trusted, SearchInput{Q: ptr("go")})
			case "recommend":
				_, err = s.Recommend(ctx, trusted, RecommendInput{})
			case "get":
				_, err = s.Get(ctx, trusted, GetInput{ArticleID: ptr(int64(1))})
			}
			want := ToolTimeout
			calls := 1
			if cancelFirst {
				want = ToolCanceled
				calls = 0
			}
			if CodeOf(err) != want || f.calls != calls {
				t.Fatalf("%s取消/重试: %v calls=%d", tool, err, f.calls)
			}
		}
	}
}

func TestFinalJSONBudgetAndMetadataTruncation(t *testing.T) {
	items := []articleDomain.ListItem{}
	for id := int64(1); id <= 10; id++ {
		item := toolArticle(id)
		item.Title = strings.Repeat("界", 300)
		item.Author.Nickname = strings.Repeat("作", 300)
		item.Excerpt = strings.Repeat("<", 1100)
		items = append(items, item)
	}
	f := &toolFixture{items: items, content: strings.Repeat("<", 8000)}
	s := NewService(f, f, f, Config{MaxOutputBytes: 15000})
	result, err := s.Search(context.Background(), trusted, SearchInput{Q: ptr("go"), Limit: ptr(10)})
	encoded, _ := json.Marshal(result)
	if err != nil || len(encoded) > 15000 || !result.Truncated || len(result.Items) == 0 || len(result.Items) == 10 {
		t.Fatalf("字节预算未截断尾部: %d %v", len(encoded), err)
	}
	for i, item := range result.Items {
		if item.ArticleID != int64(i+1) || !item.MetadataTruncated || len([]rune(item.Summary)) != 1000 || len([]rune(item.Title)) != 256 || item.Path != "/articles/"+string(rune('1'+i)) {
			t.Fatalf("身份/顺序/展示: %+v", item)
		}
	}
	get, err := s.Get(context.Background(), trusted, GetInput{ArticleID: ptr(int64(1)), MaxChars: ptr(8000)})
	encoded, _ = json.Marshal(get)
	if err != nil || len(encoded) > 15000 || !get.Truncated || len(get.Content) == 8000 || !strings.HasPrefix(f.content, get.Content) {
		t.Fatal("JSON转义后的正文预算错误", err)
	}
	item := toolArticle(1)
	item.Origin = articleDomain.OriginRSS
	item.Source = articleDomain.SourceSummary{ID: 1, Title: strings.Repeat("源", 300)}
	item.CanonicalURL = "https://example.com/a"
	item.AuthorName = ptr(strings.Repeat("作", 300))
	item.Enhancement = &articleDomain.Enhancement{Summary: "当前摘要", Method: "extractive"}
	ref, err := reference(item)
	if err != nil || !ref.MetadataTruncated || ref.OriginalURL == nil || *ref.OriginalURL != item.CanonicalURL || ref.SummarySource != "extractive" {
		t.Fatalf("RSS引用: %+v %v", ref, err)
	}
}

func TestActiveCancellationStopsBlockedTool(t *testing.T) {
	f := &toolFixture{block: true}
	s := NewService(f, f, f, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(15*time.Millisecond, cancel)
	defer timer.Stop()
	defer cancel()
	_, err := s.Recommend(ctx, trusted, RecommendInput{})
	if CodeOf(err) != ToolCanceled || f.calls != 1 {
		t.Fatalf("主动取消未传递或重复调用: %v %d", err, f.calls)
	}
}
