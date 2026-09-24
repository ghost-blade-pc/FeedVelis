package hertz

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type readyChecker struct{ err error }

func (c readyChecker) Ping(context.Context) error { return c.err }

func TestLiveAndRequestID(t *testing.T) {
	h := newTestServer(nil)
	response := ut.PerformRequest(h.Engine, consts.MethodGet, "/livez", nil)
	if response.Code != consts.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if id := string(response.Header().Peek("X-Request-ID")); len(id) != 32 {
		t.Fatalf("X-Request-ID = %q", id)
	}
}

func TestReadyFailureUsesErrorEnvelope(t *testing.T) {
	h := newTestServer(context.DeadlineExceeded)
	response := ut.PerformRequest(h.Engine, consts.MethodGet, "/readyz", nil)
	if response.Code != consts.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
	if body := response.Body.String(); !strings.Contains(body, `"code":"NOT_READY"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestNoRouteUsesErrorEnvelope(t *testing.T) {
	h := newTestServer(nil)
	response := ut.PerformRequest(h.Engine, consts.MethodGet, "/missing", nil)
	if response.Code != consts.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
	if body := response.Body.String(); !strings.Contains(body, `"code":"NOT_FOUND"`) {
		t.Fatalf("body = %s", body)
	}
}

type listRepository struct {
	items  []articleDomain.ListItem
	detail articleDomain.Detail
}

func (*listRepository) Upsert(context.Context, articleDomain.Candidate, time.Time) (articleDomain.UpsertResult, int64, error) {
	return articleDomain.UpsertInserted, 1, nil
}
func (r *listRepository) ListPublished(context.Context, *articleDomain.Cursor, int) ([]articleDomain.ListItem, error) {
	return r.items, nil
}
func (r *listRepository) GetPublished(_ context.Context, articleID int64) (articleDomain.Detail, error) {
	if r.detail.Item.ID != articleID {
		return articleDomain.Detail{}, articleDomain.ErrNotFound
	}
	return r.detail, nil
}

type noOpSanitizer struct{}

func (noOpSanitizer) Sanitize(string) ports.SanitizedContent { return ports.SanitizedContent{} }

type testClock struct{}

func (testClock) Now() time.Time { return time.Now().UTC() }

func TestArticleListContractAndInvalidCursor(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	healthService := health.NewService(readyChecker{})
	published := time.Date(2026, 9, 1, 8, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	repository := &listRepository{items: []articleDomain.ListItem{
		{
			ID: 1, Origin: articleDomain.OriginRSS, Title: "文章", CanonicalURL: "https://example.com/a",
			Source:  articleDomain.SourceSummary{ID: 2, Title: "来源"},
			Excerpt: "摘要", SourcePublishedAt: &published, DiscoveredAt: published, SortAt: published,
			Enhancement: &articleDomain.Enhancement{Summary: "<b>AI 摘要</b>", Keywords: []string{"关键词"}, Topics: []string{"主题"}, GeneratedAt: published},
		},
		{
			ID: 2, Origin: articleDomain.OriginUser, Title: "投稿", Excerpt: "投稿摘要",
			Author: &articleDomain.AuthorSummary{ID: "51000000-0000-0000-0000-000000000001", Nickname: "作者"},
			SortAt: published, DiscoveredAt: published,
		},
	}}
	service := articleApp.NewService(repository, noOpSanitizer{}, testClock{})
	h := NewServer(Options{Address: "127.0.0.1:0", ShutdownTimeout: time.Second, Logger: logger, Health: healthService, Articles: service})
	response := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/articles?limit=20", nil)
	if response.Code != consts.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var page struct {
		Items []struct {
			ID          int64           `json:"id"`
			PublishedAt time.Time       `json:"published_at"`
			Origin      json.RawMessage `json:"origin"`
			Enhancement *struct {
				Summary  string   `json:"summary"`
				Keywords []string `json:"keywords"`
				Topics   []string `json:"topics"`
			} `json:"enhancement"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 2 {
		t.Fatalf("解析失败: %v body=%s", err, response.Body.String())
	}
	if page.Items[0].Enhancement == nil || page.Items[0].Enhancement.Summary != "<b>AI 摘要</b>" || page.Items[1].Enhancement != nil {
		t.Fatalf("enhancement 契约错误: %+v", page.Items)
	}
	// RSS 条目走 rss 分支：携带 Source 摘要与原文 URL，不暴露站内作者。
	if !strings.Contains(string(page.Items[0].Origin), `"type":"rss"`) ||
		!strings.Contains(string(page.Items[0].Origin), `"canonical_url":"https://example.com/a"`) ||
		strings.Contains(string(page.Items[0].Origin), `"author"`) ||
		!page.Items[0].PublishedAt.Equal(published.UTC()) {
		t.Fatalf("RSS origin = %s", page.Items[0].Origin)
	}
	// 用户条目走 user 分支：只公开作者 ID 与昵称，不携带 Source 或原文 URL。
	if !strings.Contains(string(page.Items[1].Origin), `"type":"user"`) ||
		!strings.Contains(string(page.Items[1].Origin), `"nickname":"作者"`) ||
		strings.Contains(string(page.Items[1].Origin), "canonical_url") {
		t.Fatalf("用户 origin = %s", page.Items[1].Origin)
	}
	if body := response.Body.String(); strings.Contains(body, "content_hash") || strings.Contains(body, "discovered_at") || strings.Contains(body, "provider") || strings.Contains(body, "vector") || strings.Contains(body, "prompt") {
		t.Fatalf("不得暴露内部字段: %s", body)
	}
	invalid := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/articles?cursor=bad", nil)
	if invalid.Code != consts.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"code":"INVALID_CURSOR"`) {
		t.Fatalf("status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestArticleDetailContractAndNotFound(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	healthService := health.NewService(readyChecker{})
	published := time.Date(2026, 9, 1, 8, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	htmlValue := `<p>正文</p><img src="https://example.com/a.png" alt="图" loading="lazy"/>`
	repository := &listRepository{detail: articleDomain.Detail{
		Item: articleDomain.ListItem{
			ID: 7, Origin: articleDomain.OriginRSS, Title: "文章", CanonicalURL: "https://example.com/a",
			Source:  articleDomain.SourceSummary{ID: 2, Title: "来源"},
			Excerpt: "摘要", SourcePublishedAt: &published, DiscoveredAt: published, SortAt: published,
		},
		SanitizedHTML: &htmlValue,
	}}
	service := articleApp.NewService(repository, noOpSanitizer{}, testClock{})
	h := NewServer(Options{Address: "127.0.0.1:0", ShutdownTimeout: time.Second, Logger: logger, Health: healthService, Articles: service})
	response := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/articles/7", nil)
	var detail struct {
		ContentHTML string    `json:"content_html"`
		PublishedAt time.Time `json:"published_at"`
		Origin      struct {
			Type         string `json:"type"`
			CanonicalURL string `json:"canonical_url"`
			Source       struct {
				Title string `json:"title"`
			} `json:"source"`
		} `json:"origin"`
	}
	if response.Code != consts.StatusOK || json.Unmarshal(response.Body.Bytes(), &detail) != nil || detail.ContentHTML != htmlValue {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if detail.Origin.Type != "rss" || detail.Origin.CanonicalURL != "https://example.com/a" ||
		detail.Origin.Source.Title != "来源" || !detail.PublishedAt.Equal(published.UTC()) {
		t.Fatalf("origin = %+v published_at = %s", detail.Origin, detail.PublishedAt)
	}
	missing := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/articles/999", nil)
	if missing.Code != consts.StatusNotFound || !strings.Contains(missing.Body.String(), `"code":"ARTICLE_NOT_FOUND"`) {
		t.Fatalf("status=%d body=%s", missing.Code, missing.Body.String())
	}
	badID := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/articles/abc", nil)
	if badID.Code != consts.StatusBadRequest || !strings.Contains(badID.Body.String(), `"code":"VALIDATION_FAILED"`) {
		t.Fatalf("status=%d body=%s", badID.Code, badID.Body.String())
	}
}

func newTestServer(checkErr error) *server.Hertz {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := health.NewService(readyChecker{err: checkErr})
	return NewServer(Options{Address: "127.0.0.1:0", ShutdownTimeout: time.Second, Logger: logger, Health: service})
}
