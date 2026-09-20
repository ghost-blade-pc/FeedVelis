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
	repository := &listRepository{items: []articleDomain.ListItem{{
		ID: 1, Title: "文章", CanonicalURL: "https://example.com/a", Source: articleDomain.SourceSummary{ID: 2, Title: "来源"},
		Excerpt: "摘要", SourcePublishedAt: &published, DiscoveredAt: published, SortAt: published,
	}}}
	service := articleApp.NewService(repository, noOpSanitizer{}, testClock{})
	h := NewServer(Options{Address: "127.0.0.1:0", ShutdownTimeout: time.Second, Logger: logger, Health: healthService, Articles: service})
	response := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/articles?limit=20", nil)
	if response.Code != consts.StatusOK || !strings.Contains(response.Body.String(), `"canonical_url":"https://example.com/a"`) ||
		!strings.Contains(response.Body.String(), `"discovered_at":"2026-09-01T00:00:00Z"`) || strings.Contains(response.Body.String(), "content_hash") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
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
			ID: 7, Title: "文章", CanonicalURL: "https://example.com/a", Source: articleDomain.SourceSummary{ID: 2, Title: "来源"},
			Excerpt: "摘要", SourcePublishedAt: &published, DiscoveredAt: published, SortAt: published,
		},
		SanitizedHTML: &htmlValue,
	}}
	service := articleApp.NewService(repository, noOpSanitizer{}, testClock{})
	h := NewServer(Options{Address: "127.0.0.1:0", ShutdownTimeout: time.Second, Logger: logger, Health: healthService, Articles: service})
	response := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/articles/7", nil)
	var detail struct {
		ContentHTML string `json:"content_html"`
	}
	if response.Code != consts.StatusOK || json.Unmarshal(response.Body.Bytes(), &detail) != nil || detail.ContentHTML != htmlValue {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
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
