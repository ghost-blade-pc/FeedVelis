package hertz

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type searcherFake struct {
	request searchApp.Request
	page    searchApp.Page
	err     error
}

func (f *searcherFake) Search(_ context.Context, request searchApp.Request) (searchApp.Page, error) {
	f.request = request
	return f.page, f.err
}

func TestSearchRouteContractAndParameterBinding(t *testing.T) {
	next := "opaque-cursor"
	published := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	fake := &searcherFake{page: searchApp.Page{Items: []articleDomain.ListItem{{
		ID: 7, Origin: articleDomain.OriginRSS, Title: "搜索结果", Excerpt: "摘要", SortAt: published,
		CanonicalURL: "https://example.com/7", Source: articleDomain.SourceSummary{ID: 9, Title: "来源"},
	}}, NextCursor: &next, HasMore: true}}
	h := searchServer(fake)
	response := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/search/articles?q=%E4%B8%AD%E6%96%87+Go&keyword=%E5%85%B3%E9%94%AE%E8%AF%8D&topic=%E4%B8%BB%E9%A2%98&source_id=9&limit=10&cursor=incoming", nil)
	if response.Code != consts.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.request.Q != "中文 Go" || fake.request.Keyword == nil || *fake.request.Keyword != "关键词" || fake.request.Topic == nil || *fake.request.Topic != "主题" || fake.request.SourceID == nil || *fake.request.SourceID != 9 || fake.request.Limit != 10 || fake.request.Cursor != "incoming" {
		t.Fatalf("绑定结果错误: %+v", fake.request)
	}
	body := response.Body.String()
	for _, required := range []string{`"items"`, `"next_cursor":"opaque-cursor"`, `"has_more":true`, `"id":7`} {
		if !strings.Contains(body, required) {
			t.Fatalf("响应缺少 %s: %s", required, body)
		}
	}
	for _, forbidden := range []string{"score", "pit", "_index", "plain_text", "vector"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("响应泄露内部字段 %q: %s", forbidden, body)
		}
	}
}

func TestSearchRouteAlwaysExistsAndMapsControlledErrors(t *testing.T) {
	unconfigured := searchServer(nil)
	response := ut.PerformRequest(unconfigured.Engine, consts.MethodGet, "/api/v1/search/articles?q=go", nil)
	if response.Code != consts.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"SEARCH_UNAVAILABLE"`) || string(response.Header().Peek("Retry-After")) != "1" {
		t.Fatalf("未配置搜索响应错误: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"request_id"`) || len(response.Header().Peek("X-Request-ID")) != 32 {
		t.Fatalf("错误信封缺少 request_id: %s", response.Body.String())
	}

	cases := []struct {
		name string
		err  error
		code int
		body string
	}{
		{"validation", &searchApp.Error{Code: searchApp.CodeValidationFailed}, 400, "VALIDATION_FAILED"},
		{"cursor", &searchApp.Error{Code: searchApp.CodeInvalidCursor}, 400, "INVALID_CURSOR"},
		{"search", &searchApp.Error{Code: searchApp.CodeSearchUnavailable}, 503, "SEARCH_UNAVAILABLE"},
		{"postgres", &searchApp.Error{Code: searchApp.CodeDependencyUnavailable}, 503, "DEPENDENCY_UNAVAILABLE"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			server := searchServer(&searcherFake{err: item.err})
			got := ut.PerformRequest(server.Engine, consts.MethodGet, "/api/v1/search/articles?q=go", nil)
			if got.Code != item.code || !strings.Contains(got.Body.String(), `"code":"`+item.body+`"`) {
				t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
			}
		})
	}
}

func TestSearchRouteRejectsMalformedNumericParametersBeforeService(t *testing.T) {
	for _, path := range []string{
		"/api/v1/search/articles?q=go&source_id=abc",
		"/api/v1/search/articles?q=go&source_id=",
		"/api/v1/search/articles?q=go&limit=abc",
		"/api/v1/search/articles?q=go&limit=",
	} {
		fake := &searcherFake{}
		response := ut.PerformRequest(searchServer(fake).Engine, consts.MethodGet, path, nil)
		if response.Code != 400 || !strings.Contains(response.Body.String(), "VALIDATION_FAILED") || fake.request.Q != "" {
			t.Fatalf("%s: status=%d request=%+v body=%s", path, response.Code, fake.request, response.Body.String())
		}
	}
}

func TestSearchFailureDoesNotAffectLatestDetailOrReadiness(t *testing.T) {
	published := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	repository := &listRepository{
		items:  []articleDomain.ListItem{{ID: 1, Origin: articleDomain.OriginRSS, Title: "latest", SortAt: published, CanonicalURL: "https://example.com/1", Source: articleDomain.SourceSummary{ID: 2, Title: "source"}}},
		detail: articleDomain.Detail{Item: articleDomain.ListItem{ID: 1, Origin: articleDomain.OriginRSS, Title: "detail", SortAt: published, CanonicalURL: "https://example.com/1", Source: articleDomain.SourceSummary{ID: 2, Title: "source"}}},
	}
	articles := articleApp.NewService(repository, noOpSanitizer{}, testClock{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewServer(Options{Address: "127.0.0.1:0", ShutdownTimeout: time.Second, Logger: logger,
		Health: health.NewService(readyChecker{}), Articles: articles,
		Search: &searcherFake{err: &searchApp.Error{Code: searchApp.CodeSearchUnavailable}},
	})
	search := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/search/articles?q=go", nil)
	latest := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/articles", nil)
	detail := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/articles/1", nil)
	ready := ut.PerformRequest(h.Engine, consts.MethodGet, "/readyz", nil)
	if search.Code != 503 || latest.Code != 200 || detail.Code != 200 || ready.Code != 200 {
		t.Fatalf("故障隔离失败: search=%d latest=%d detail=%d ready=%d", search.Code, latest.Code, detail.Code, ready.Code)
	}
}

func searchServer(search searchApp.Searcher) *server.Hertz {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer(Options{Address: "127.0.0.1:0", ShutdownTimeout: time.Second, Logger: logger,
		Health: health.NewService(readyChecker{}), Search: search})
}
