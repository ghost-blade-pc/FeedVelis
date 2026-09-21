package hertz

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/dto"
)

const validKey = "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c11"

type fakeUserArticles struct {
	result  articleApp.UserArticleResult
	detail  articleApp.UserArticleDetail
	page    articleApp.UserArticlePage
	preview articleApp.ArticlePreview
	err     error

	lastCreate     articleApp.CreateUserArticleCommand
	lastUpdate     articleApp.UpdateUserArticleCommand
	lastState      articleApp.UserArticleStateCommand
	lastPreview    articleApp.PreviewArticleCommand
	lastListCursor string
	lastListLimit  int
	stateCalls     int
}

func (f *fakeUserArticles) Create(_ context.Context, command articleApp.CreateUserArticleCommand) (articleApp.UserArticleResult, bool, error) {
	f.lastCreate = command
	return f.result, false, f.err
}

func (f *fakeUserArticles) Get(_ context.Context, authorID string, articleID int64) (articleApp.UserArticleDetail, error) {
	if authorID != testUser().ID.String() || articleID != f.detail.Article.ID {
		return articleApp.UserArticleDetail{}, articleDomain.ErrNotFound
	}
	return f.detail, f.err
}

func (f *fakeUserArticles) List(_ context.Context, _ string, cursor string, limit int) (articleApp.UserArticlePage, error) {
	f.lastListCursor, f.lastListLimit = cursor, limit
	return f.page, f.err
}

func (f *fakeUserArticles) Update(_ context.Context, command articleApp.UpdateUserArticleCommand) (articleApp.UserArticleResult, bool, error) {
	f.lastUpdate = command
	return f.result, false, f.err
}

func (f *fakeUserArticles) Publish(_ context.Context, command articleApp.UserArticleStateCommand) (articleApp.UserArticleResult, bool, error) {
	f.lastState, f.stateCalls = command, f.stateCalls+1
	return f.result, false, f.err
}

func (f *fakeUserArticles) Offline(_ context.Context, command articleApp.UserArticleStateCommand) (articleApp.UserArticleResult, bool, error) {
	f.lastState, f.stateCalls = command, f.stateCalls+1
	return f.result, false, f.err
}

func (f *fakeUserArticles) Delete(_ context.Context, command articleApp.UserArticleStateCommand) (articleApp.UserArticleResult, bool, error) {
	f.lastState, f.stateCalls = command, f.stateCalls+1
	return f.result, false, f.err
}

func (f *fakeUserArticles) Preview(_ context.Context, command articleApp.PreviewArticleCommand) (articleApp.ArticlePreview, error) {
	f.lastPreview = command
	return f.preview, f.err
}

const (
	testMarkdown = "# 标题\n\n正文"
	testHTML     = "<h1>标题</h1><p>正文</p>"
)

func storedArticle(status articleDomain.Status) articleDomain.StoredArticle {
	markdown, html := testMarkdown, testHTML
	return articleDomain.StoredArticle{
		ID: 42, Origin: articleDomain.OriginUser, Status: status,
		RevisionID: 7, RevisionNumber: 2, LockVersion: 3,
		UpdatedAt: time.Date(2026, 9, 21, 4, 0, 0, 0, time.UTC),
		Revision: articleDomain.RevisionData{
			Title: "标题", Markdown: &markdown, SanitizedHTML: &html,
			PlainText: "标题正文", Excerpt: "标题正文",
		},
	}
}

func newMyArticleServer(t *testing.T, articles *fakeUserArticles) *server.Hertz {
	t.Helper()
	account := &fakeAccount{identity: accountApp.Identity{
		User:    testUser(),
		Session: accountDomain.NewSession(accountDomain.UUID{2}, accountDomain.UUID{1}, time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC), time.Hour),
	}}
	return NewServer(Options{
		Address: "127.0.0.1:0", ShutdownTimeout: time.Second,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health: health.NewService(readyChecker{}),
		Auth: &AuthOptions{
			Service:        account,
			AllowedOrigin:  testOrigin,
			CookieSecure:   false,
			TrustedProxies: []*net.IPNet{},
			Clock:          testClock{},
			MyArticles:     articles,
		},
	})
}

// myRequest 默认携带 Bearer 身份与 JSON 正文，额外头部由调用方按端点补充。
func myRequest(h *server.Hertz, method, path, body string, headers ...ut.Header) *ut.ResponseRecorder {
	base := []ut.Header{
		{Key: "Content-Type", Value: "application/json"},
		{Key: "Authorization", Value: "Bearer access-token"},
	}
	return ut.PerformRequest(h.Engine, method, path,
		&ut.Body{Body: strings.NewReader(body), Len: len(body)}, append(base, headers...)...)
}

func TestCreateMyArticleSuccessAndHeaders(t *testing.T) {
	articles := &fakeUserArticles{result: articleApp.UserArticleResult{Article: storedArticle(articleDomain.StatusDraft)}}
	h := newMyArticleServer(t, articles)
	response := myRequest(h, consts.MethodPost, "/api/v1/me/articles",
		`{"title":"标题","markdown":"正文","initial_status":"draft"}`,
		ut.Header{Key: "Idempotency-Key", Value: strings.ToUpper(validKey)})

	if response.Code != consts.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if etag := string(response.Header().Peek("ETag")); etag != `"3"` {
		t.Fatalf("ETag = %q", etag)
	}
	var body dto.MyArticleDetail
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "draft" || body.RevisionNo != 2 || body.LockVersion != 3 ||
		body.ContentHTML != testHTML || body.Markdown != testMarkdown || body.Title != "标题" {
		t.Fatalf("响应 = %+v", body)
	}
	if body.AssetIDs == nil || len(body.AssetIDs) != 0 {
		t.Fatalf("asset_ids 必须是空数组而不是 null: %s", response.Body.String())
	}
	// 幂等键规范化后传给用例，大小写不同的同一 UUID 不得产生第二个操作身份。
	if articles.lastCreate.IdempotencyKey != validKey || articles.lastCreate.AuthorUserID != testUser().ID.String() {
		t.Fatalf("命令 = %+v", articles.lastCreate)
	}
	if articles.lastCreate.InitialStatus != articleDomain.StatusDraft || articles.lastCreate.Title != "标题" {
		t.Fatalf("命令 = %+v", articles.lastCreate)
	}
}

func TestCreateMyArticleRejectsUnknownFieldAndOversizeBody(t *testing.T) {
	articles := &fakeUserArticles{result: articleApp.UserArticleResult{Article: storedArticle(articleDomain.StatusDraft)}}
	h := newMyArticleServer(t, articles)
	cases := map[string]string{
		"未知字段": `{"title":"标题","markdown":"正文","initial_status":"draft","extra":1}`,
		"重复字段": `{"title":"标题","title":"标题","markdown":"正文","initial_status":"draft"}`,
		"非法状态": `{"title":"标题","markdown":"正文","initial_status":"offline"}`,
		"正文超限": `{"title":"标题","markdown":"` + strings.Repeat("a", 400<<10) + `","initial_status":"draft"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			response := myRequest(h, consts.MethodPost, "/api/v1/me/articles", body,
				ut.Header{Key: "Idempotency-Key", Value: validKey})
			if response.Code != consts.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"VALIDATION_FAILED"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestWriteEndpointsRequireHeaders(t *testing.T) {
	articles := &fakeUserArticles{result: articleApp.UserArticleResult{Article: storedArticle(articleDomain.StatusDraft)}}
	h := newMyArticleServer(t, articles)

	missingKey := myRequest(h, consts.MethodPost, "/api/v1/me/articles",
		`{"title":"标题","markdown":"正文","initial_status":"draft"}`)
	if missingKey.Code != consts.StatusBadRequest || !strings.Contains(missingKey.Body.String(), `"code":"IDEMPOTENCY_KEY_REQUIRED"`) {
		t.Fatalf("缺少幂等键: status=%d body=%s", missingKey.Code, missingKey.Body.String())
	}

	invalidKey := myRequest(h, consts.MethodPost, "/api/v1/me/articles",
		`{"title":"标题","markdown":"正文","initial_status":"draft"}`,
		ut.Header{Key: "Idempotency-Key", Value: "not-a-uuid"})
	if invalidKey.Code != consts.StatusBadRequest || !strings.Contains(invalidKey.Body.String(), `"code":"IDEMPOTENCY_KEY_INVALID"`) {
		t.Fatalf("非法幂等键: status=%d body=%s", invalidKey.Code, invalidKey.Body.String())
	}

	missingMatch := myRequest(h, consts.MethodPatch, "/api/v1/me/articles/42", `{"title":"标题","markdown":"正文"}`,
		ut.Header{Key: "Idempotency-Key", Value: validKey})
	if missingMatch.Code != consts.StatusBadRequest || !strings.Contains(missingMatch.Body.String(), `"code":"IF_MATCH_REQUIRED"`) {
		t.Fatalf("缺少 If-Match: status=%d body=%s", missingMatch.Code, missingMatch.Body.String())
	}

	weakMatch := myRequest(h, consts.MethodPatch, "/api/v1/me/articles/42", `{"title":"标题","markdown":"正文"}`,
		ut.Header{Key: "Idempotency-Key", Value: validKey}, ut.Header{Key: "If-Match", Value: `W/"3"`})
	if weakMatch.Code != consts.StatusBadRequest || !strings.Contains(weakMatch.Body.String(), `"code":"IF_MATCH_INVALID"`) {
		t.Fatalf("弱 If-Match: status=%d body=%s", weakMatch.Code, weakMatch.Body.String())
	}
	if articles.stateCalls != 0 {
		t.Fatal("头部被拒绝时不得进入用例")
	}
}

func TestUpdateMyArticlePassesParsedVersion(t *testing.T) {
	articles := &fakeUserArticles{result: articleApp.UserArticleResult{Article: storedArticle(articleDomain.StatusPublished)}}
	h := newMyArticleServer(t, articles)
	response := myRequest(h, consts.MethodPatch, "/api/v1/me/articles/42", `{"title":"新标题","markdown":"新正文"}`,
		ut.Header{Key: "Idempotency-Key", Value: validKey}, ut.Header{Key: "If-Match", Value: `"3"`})
	if response.Code != consts.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if articles.lastUpdate.ArticleID != 42 || articles.lastUpdate.ExpectedVersion != 3 ||
		articles.lastUpdate.Title != "新标题" || articles.lastUpdate.IdempotencyKey != validKey {
		t.Fatalf("命令 = %+v", articles.lastUpdate)
	}
}

func TestMyArticleErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		method   string
		path     string
		body     string
		wantCode int
		wantBody string
	}{
		{"版本冲突", articleDomain.ErrVersionConflict, consts.MethodPatch, "/api/v1/me/articles/42",
			`{"title":"标题","markdown":"正文"}`, consts.StatusConflict, `"code":"ARTICLE_VERSION_CONFLICT"`},
		{"管理员锁定", articleDomain.ErrAdminOffline, consts.MethodPost, "/api/v1/me/articles/42/publish",
			"", consts.StatusForbidden, `"code":"ARTICLE_ADMIN_OFFLINE"`},
		{"状态不允许", articleDomain.ErrInvalidState, consts.MethodPost, "/api/v1/me/articles/42/offline",
			"", consts.StatusConflict, `"code":"ARTICLE_INVALID_STATE"`},
		{"幂等键复用", idempotencyApp.ErrKeyReused, consts.MethodPost, "/api/v1/me/articles",
			`{"title":"标题","markdown":"正文","initial_status":"draft"}`, consts.StatusConflict, `"code":"IDEMPOTENCY_KEY_REUSED"`},
		{"内容无效", ports.InvalidContent(errors.New("标题不能超过 200 个 Unicode 字符")), consts.MethodPost, "/api/v1/me/articles",
			`{"title":"标题","markdown":"正文","initial_status":"draft"}`, consts.StatusBadRequest, `"code":"ARTICLE_CONTENT_INVALID"`},
		{"归属不匹配", articleDomain.ErrNotFound, consts.MethodGet, "/api/v1/me/articles/99",
			"", consts.StatusNotFound, `"code":"ARTICLE_NOT_FOUND"`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newMyArticleServer(t, &fakeUserArticles{err: testCase.err})
			response := myRequest(h, testCase.method, testCase.path, testCase.body,
				ut.Header{Key: "Idempotency-Key", Value: validKey}, ut.Header{Key: "If-Match", Value: `"3"`})
			if response.Code != testCase.wantCode || !strings.Contains(response.Body.String(), testCase.wantBody) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestMyArticleLifecycleSuccessResponses(t *testing.T) {
	articles := &fakeUserArticles{result: articleApp.UserArticleResult{Article: storedArticle(articleDomain.StatusPublished)}}
	h := newMyArticleServer(t, articles)

	for _, path := range []string{"/api/v1/me/articles/42/publish", "/api/v1/me/articles/42/offline"} {
		response := myRequest(h, consts.MethodPost, path, "",
			ut.Header{Key: "Idempotency-Key", Value: validKey}, ut.Header{Key: "If-Match", Value: `"3"`})
		if response.Code != consts.StatusOK || string(response.Header().Peek("ETag")) != `"3"` {
			t.Fatalf("%s: status=%d etag=%q body=%s", path, response.Code, response.Header().Peek("ETag"), response.Body.String())
		}
	}

	deleted := myRequest(h, consts.MethodDelete, "/api/v1/me/articles/42", "",
		ut.Header{Key: "Idempotency-Key", Value: validKey}, ut.Header{Key: "If-Match", Value: `"3"`})
	if deleted.Code != consts.StatusNoContent || len(deleted.Body.Bytes()) != 0 {
		t.Fatalf("删除: status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	if articles.stateCalls != 3 || articles.lastState.ExpectedVersion != 3 {
		t.Fatalf("状态变更调用 = %d 命令 = %+v", articles.stateCalls, articles.lastState)
	}
}

func TestGetMyArticleReturnsPrivateDetail(t *testing.T) {
	articles := &fakeUserArticles{detail: articleApp.UserArticleDetail{
		Article: storedArticle(articleDomain.StatusDraft), AssetIDs: []string{},
	}}
	h := newMyArticleServer(t, articles)
	response := myRequest(h, consts.MethodGet, "/api/v1/me/articles/42", "")
	if response.Code != consts.StatusOK || string(response.Header().Peek("ETag")) != `"3"` {
		t.Fatalf("status=%d etag=%q body=%s", response.Code, response.Header().Peek("ETag"), response.Body.String())
	}
	if body := response.Body.String(); !strings.Contains(body, `"markdown":"# 标题\n\n正文"`) || !strings.Contains(body, `"status":"draft"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestListMyArticlesPassesCursorAndLimit(t *testing.T) {
	articles := &fakeUserArticles{page: articleApp.UserArticlePage{
		Items: []articleDomain.StoredArticle{storedArticle(articleDomain.StatusPublished)},
	}}
	h := newMyArticleServer(t, articles)
	response := myRequest(h, consts.MethodGet, "/api/v1/me/articles?limit=5&cursor=abc", "")
	if response.Code != consts.StatusOK || articles.lastListLimit != 5 || articles.lastListCursor != "abc" {
		t.Fatalf("status=%d limit=%d cursor=%q", response.Code, articles.lastListLimit, articles.lastListCursor)
	}
	if body := response.Body.String(); !strings.Contains(body, `"has_more":false`) || !strings.Contains(body, `"next_cursor":null`) {
		t.Fatalf("body = %s", body)
	}

	invalid := myRequest(h, consts.MethodGet, "/api/v1/me/articles?limit=abc", "")
	if invalid.Code != consts.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"code":"VALIDATION_FAILED"`) {
		t.Fatalf("status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestPreviewMyArticleIsStateless(t *testing.T) {
	articles := &fakeUserArticles{preview: articleApp.ArticlePreview{
		ContentHTML: "<p>预览</p>", PlainText: "预览", Excerpt: "预览",
		AssetIDs: []string{"6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c11"},
	}}
	h := newMyArticleServer(t, articles)
	response := myRequest(h, consts.MethodPost, "/api/v1/me/articles/preview", `{"title":"标题","markdown":"预览"}`)
	if response.Code != consts.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body dto.ArticlePreview
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ContentHTML != "<p>预览</p>" || body.PlainText != "预览" || body.Excerpt != "预览" ||
		len(body.AssetIDs) != 1 || body.AssetIDs[0] != validKey {
		t.Fatalf("响应 = %+v", body)
	}
	if articles.lastPreview.Title != "标题" || articles.lastPreview.Markdown != "预览" {
		t.Fatalf("命令 = %+v", articles.lastPreview)
	}

	unknown := myRequest(h, consts.MethodPost, "/api/v1/me/articles/preview", `{"title":"标题","markdown":"预览","extra":1}`)
	if unknown.Code != consts.StatusBadRequest {
		t.Fatalf("未知字段: status=%d body=%s", unknown.Code, unknown.Body.String())
	}
}

func TestMyArticleRoutesRequireAuthentication(t *testing.T) {
	articles := &fakeUserArticles{result: articleApp.UserArticleResult{Article: storedArticle(articleDomain.StatusDraft)}}
	h := newMyArticleServer(t, articles)
	requests := []struct{ method, path, body string }{
		{consts.MethodGet, "/api/v1/me/articles", ""},
		{consts.MethodPost, "/api/v1/me/articles", `{"title":"标题","markdown":"正文","initial_status":"draft"}`},
		{consts.MethodPost, "/api/v1/me/articles/preview", `{"title":"标题","markdown":"正文"}`},
		{consts.MethodGet, "/api/v1/me/articles/42", ""},
		{consts.MethodDelete, "/api/v1/me/articles/42", ""},
	}
	for _, request := range requests {
		response := ut.PerformRequest(h.Engine, request.method, request.path,
			&ut.Body{Body: strings.NewReader(request.body), Len: len(request.body)},
			ut.Header{Key: "Content-Type", Value: "application/json"},
			ut.Header{Key: "Idempotency-Key", Value: validKey}, ut.Header{Key: "If-Match", Value: `"3"`})
		if response.Code != consts.StatusUnauthorized || !strings.Contains(response.Body.String(), `"code":"AUTH_SESSION_INVALID"`) {
			t.Fatalf("%s %s: status=%d body=%s", request.method, request.path, response.Code, response.Body.String())
		}
	}
}
