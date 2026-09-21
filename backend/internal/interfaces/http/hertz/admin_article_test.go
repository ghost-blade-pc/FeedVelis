package hertz

import (
	"context"
	"encoding/json"
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
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/dto"
)

type fakeAdminArticles struct {
	result  articleApp.UserArticleResult
	err     error
	lastCmd articleApp.AdminArticleCommand
	calls   int
}

func (f *fakeAdminArticles) Offline(_ context.Context, command articleApp.AdminArticleCommand) (articleApp.UserArticleResult, bool, error) {
	f.lastCmd, f.calls = command, f.calls+1
	return f.result, false, f.err
}

func (f *fakeAdminArticles) Restore(_ context.Context, command articleApp.AdminArticleCommand) (articleApp.UserArticleResult, bool, error) {
	f.lastCmd, f.calls = command, f.calls+1
	return f.result, false, f.err
}

func newAdminArticleServer(t *testing.T, role accountDomain.Role, admin *fakeAdminArticles) *server.Hertz {
	t.Helper()
	user := testUser()
	user.Role = role
	account := &fakeAccount{identity: accountApp.Identity{
		User:    user,
		Session: accountDomain.NewSession(accountDomain.UUID{2}, accountDomain.UUID{1}, time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC), time.Hour),
	}}
	return NewServer(Options{
		Address: "127.0.0.1:0", ShutdownTimeout: time.Second,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health: health.NewService(readyChecker{}),
		Auth: &AuthOptions{
			Service:        account,
			AllowedOrigin:  testOrigin,
			TrustedProxies: []*net.IPNet{},
			Clock:          testClock{},
			AdminArticles:  admin,
		},
	})
}

func adminRequest(h *server.Hertz, method, path string, headers ...ut.Header) *ut.ResponseRecorder {
	base := []ut.Header{
		{Key: "Authorization", Value: "Bearer access-token"},
		{Key: "Idempotency-Key", Value: validKey},
		{Key: "If-Match", Value: `"3"`},
	}
	return ut.PerformRequest(h.Engine, method, path, nil, append(base, headers...)...)
}

func adminOfflineArticle() articleDomain.StoredArticle {
	publishedAt := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	stored := storedArticle(articleDomain.StatusOffline)
	stored.PublishedAt = &publishedAt
	reason := articleDomain.OfflineByAdmin
	stored.OfflineReason = &reason
	return stored
}

func TestAdminOfflineAndRestoreSuccess(t *testing.T) {
	admin := &fakeAdminArticles{result: articleApp.UserArticleResult{Article: adminOfflineArticle()}}
	h := newAdminArticleServer(t, accountDomain.RoleAdmin, admin)

	for _, path := range []string{"/api/v1/admin/articles/42/offline", "/api/v1/admin/articles/42/restore"} {
		response := adminRequest(h, consts.MethodPost, path)
		if response.Code != consts.StatusOK || string(response.Header().Peek("ETag")) != `"3"` {
			t.Fatalf("%s: status=%d etag=%q body=%s", path, response.Code, response.Header().Peek("ETag"), response.Body.String())
		}
		var body dto.AdminArticleResult
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Status != "offline" || body.OfflineReason == nil || *body.OfflineReason != "admin" ||
			body.LockVersion != 3 || body.PublishedAt.Location() != time.UTC {
			t.Fatalf("响应 = %+v", body)
		}
		if strings.Contains(response.Body.String(), "markdown") || strings.Contains(response.Body.String(), "content_html") {
			t.Fatal("管理员响应不得包含内容或草稿字段")
		}
	}
	if admin.calls != 2 || admin.lastCmd.ArticleID != 42 || admin.lastCmd.ExpectedVersion != 3 {
		t.Fatalf("调用 = %d 命令 = %+v", admin.calls, admin.lastCmd)
	}
	if admin.lastCmd.Actor.Role != accountDomain.RoleAdmin || admin.lastCmd.Actor.UserID != testUser().ID.String() {
		t.Fatalf("操作者 = %+v", admin.lastCmd.Actor)
	}
}

func TestAdminArticleRejectsRegularUser(t *testing.T) {
	admin := &fakeAdminArticles{result: articleApp.UserArticleResult{Article: adminOfflineArticle()}}
	h := newAdminArticleServer(t, accountDomain.RoleUser, admin)
	for _, path := range []string{"/api/v1/admin/articles/42/offline", "/api/v1/admin/articles/42/restore"} {
		response := adminRequest(h, consts.MethodPost, path)
		if response.Code != consts.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"FORBIDDEN"`) {
			t.Fatalf("%s: status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	if admin.calls != 0 {
		t.Fatal("普通用户不得进入管理员用例")
	}
}

func TestAdminArticleErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
		wantBody string
	}{
		{"状态不允许", articleDomain.ErrInvalidState, consts.StatusConflict, `"code":"ARTICLE_INVALID_STATE"`},
		{"版本冲突", articleDomain.ErrVersionConflict, consts.StatusConflict, `"code":"ARTICLE_VERSION_CONFLICT"`},
		{"文章不存在", articleDomain.ErrNotFound, consts.StatusNotFound, `"code":"ARTICLE_NOT_FOUND"`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newAdminArticleServer(t, accountDomain.RoleAdmin, &fakeAdminArticles{err: testCase.err})
			response := adminRequest(h, consts.MethodPost, "/api/v1/admin/articles/42/offline")
			if response.Code != testCase.wantCode || !strings.Contains(response.Body.String(), testCase.wantBody) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAdminArticleRequiresHeaders(t *testing.T) {
	admin := &fakeAdminArticles{result: articleApp.UserArticleResult{Article: adminOfflineArticle()}}
	h := newAdminArticleServer(t, accountDomain.RoleAdmin, admin)
	response := ut.PerformRequest(h.Engine, consts.MethodPost, "/api/v1/admin/articles/42/offline", nil,
		ut.Header{Key: "Authorization", Value: "Bearer access-token"})
	if response.Code != consts.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"IDEMPOTENCY_KEY_REQUIRED"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if admin.calls != 0 {
		t.Fatal("缺少头部时不得进入用例")
	}
}

// TestAuthDisabledRouteMatrix 固化装配语义：auth 关闭时匿名读取存在，写路由整体不注册。
func TestAuthDisabledRouteMatrix(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	articles := articleApp.NewService(&listRepository{}, noOpSanitizer{}, testClock{})
	h := NewServer(Options{Address: "127.0.0.1:0", ShutdownTimeout: time.Second, Logger: logger,
		Health: health.NewService(readyChecker{}), Articles: articles})

	anonymous := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/articles", nil)
	if anonymous.Code != consts.StatusOK {
		t.Fatalf("匿名 latest: status=%d body=%s", anonymous.Code, anonymous.Body.String())
	}

	for _, path := range []string{"/api/v1/me/articles", "/api/v1/me/articles/42", "/api/v1/me/articles/preview",
		"/api/v1/admin/articles/42/offline", "/api/v1/admin/articles/42/restore"} {
		method := consts.MethodGet
		if strings.HasSuffix(path, "offline") || strings.HasSuffix(path, "restore") || strings.Contains(path, "preview") {
			method = consts.MethodPost
		}
		response := ut.PerformRequest(h.Engine, method, path, nil)
		if response.Code != consts.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"NOT_FOUND"`) {
			t.Fatalf("%s %s: status=%d body=%s", method, path, response.Code, response.Body.String())
		}
	}
}
