package hertz

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	feedback "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/articlefeedback"
)

type fakeFeedback struct {
	user      string
	id        int64
	operation string
	enabled   bool
	err       error
	ids       []int64
}

func (f *fakeFeedback) RecordRead(_ context.Context, user string, id int64) error {
	f.user = user
	f.id = id
	f.operation = "read"
	return f.err
}
func (f *fakeFeedback) SetFavorite(_ context.Context, user string, id int64, enabled bool) error {
	f.user = user
	f.id = id
	f.operation = "favorite"
	f.enabled = enabled
	return f.err
}
func (f *fakeFeedback) SetNotInterested(_ context.Context, user string, id int64, enabled bool) error {
	f.user = user
	f.id = id
	f.operation = "negative"
	f.enabled = enabled
	return f.err
}
func (f *fakeFeedback) States(_ context.Context, user string, ids []int64) ([]feedback.State, error) {
	f.user = user
	f.ids = ids
	return []feedback.State{{ArticleID: ids[0]}}, f.err
}

func newFeedbackServer(f *fakeFeedback) *server.Hertz {
	return newFeedbackServerForUser(f, testUser())
}

func newFeedbackServerForUser(f *fakeFeedback, user accountDomain.User) *server.Hertz {
	account := &fakeAccount{identity: accountApp.Identity{User: user, Session: accountDomain.NewSession(accountDomain.UUID{2}, user.ID, time.Now().UTC(), time.Hour)}}
	return NewServer(Options{Address: "127.0.0.1:0", ShutdownTimeout: time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Health: health.NewService(readyChecker{}), Auth: &AuthOptions{Service: account, Feedback: f}})
}

func TestArticleFeedbackUsesAuthenticatedIdentity(t *testing.T) {
	f := &fakeFeedback{}
	a := myRequest(newFeedbackServer(f), consts.MethodGet, "/api/v1/me/article-feedback?article_ids=42", "")
	if a.Code != consts.StatusOK || f.user != testUser().ID.String() {
		t.Fatalf("用户 A 身份错误: %d %s", a.Code, f.user)
	}
	b := testUser()
	b.ID = accountDomain.UUID{3}
	response := myRequest(newFeedbackServerForUser(f, b), consts.MethodGet, "/api/v1/me/article-feedback?article_ids=42&user_id="+testUser().ID.String(), "")
	if response.Code != consts.StatusOK || f.user != b.ID.String() {
		t.Fatalf("请求参数覆盖了认证身份: %d %s", response.Code, f.user)
	}
}

func TestArticleFeedbackRoutes(t *testing.T) {
	f := &fakeFeedback{}
	h := newFeedbackServer(f)
	for _, tc := range []struct {
		method, path, operation string
		enabled                 bool
	}{
		{consts.MethodPost, "/api/v1/me/articles/42/reads", "read", false},
		{consts.MethodPut, "/api/v1/me/articles/42/favorite", "favorite", true},
		{consts.MethodDelete, "/api/v1/me/articles/42/favorite", "favorite", false},
		{consts.MethodPut, "/api/v1/me/articles/42/not-interested", "negative", true},
		{consts.MethodDelete, "/api/v1/me/articles/42/not-interested", "negative", false},
	} {
		response := myRequest(h, tc.method, tc.path, "")
		if response.Code != consts.StatusNoContent || response.Body.Len() != 0 || f.operation != tc.operation || f.enabled != tc.enabled || f.id != 42 || f.user != testUser().ID.String() {
			t.Fatalf("%s %s: status=%d body=%s fake=%+v", tc.method, tc.path, response.Code, response.Body.String(), f)
		}
	}
	response := myRequest(h, consts.MethodGet, "/api/v1/me/article-feedback?article_ids=42,43", "")
	if response.Code != consts.StatusOK || !strings.Contains(response.Body.String(), `"items"`) || len(f.ids) != 2 || f.ids[1] != 43 {
		t.Fatalf("批量状态: %d %s %+v", response.Code, response.Body.String(), f)
	}
}

func TestArticleFeedbackValidationAndAuth(t *testing.T) {
	f := &fakeFeedback{}
	h := newFeedbackServer(f)
	for _, path := range []string{"/api/v1/me/articles/0/reads", "/api/v1/me/articles/-1/reads", "/api/v1/me/articles/no/reads", "/api/v1/me/articles/+1/reads", "/api/v1/me/articles/01/reads"} {
		response := myRequest(h, consts.MethodPost, path, "")
		if response.Code != consts.StatusBadRequest {
			t.Fatalf("非法文章 ID: %s %d", path, response.Code)
		}
	}
	response := myRequest(h, consts.MethodPut, "/api/v1/me/articles/42/favorite", `{}`)
	if response.Code != consts.StatusBadRequest {
		t.Fatalf("意外正文: %d", response.Code)
	}
	for _, query := range []string{"", "1,1", "0", "1,foo", "+1", "01", listIDs(51)} {
		response := myRequest(h, consts.MethodGet, "/api/v1/me/article-feedback?article_ids="+query, "")
		if response.Code != consts.StatusBadRequest {
			t.Fatalf("非法列表 %q: %d %s", query, response.Code, response.Body.String())
		}
	}
	response = myRequest(h, consts.MethodGet, "/api/v1/me/article-feedback?article_ids="+listIDs(50), "")
	if response.Code != consts.StatusOK {
		t.Fatalf("50 ID 应通过: %d %s", response.Code, response.Body.String())
	}
	response = ut.PerformRequest(h.Engine, consts.MethodPost, "/api/v1/me/articles/42/reads", nil)
	if response.Code != consts.StatusUnauthorized {
		t.Fatalf("匿名请求: %d", response.Code)
	}
	f.err = feedback.ErrArticleNotFound
	response = myRequest(h, consts.MethodPost, "/api/v1/me/articles/42/reads", "")
	if response.Code != consts.StatusNotFound || !strings.Contains(response.Body.String(), "ARTICLE_NOT_FOUND") {
		t.Fatalf("不可见文章: %d %s", response.Code, response.Body.String())
	}
	withoutAuth := NewServer(Options{Address: "127.0.0.1:0", ShutdownTimeout: time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Health: health.NewService(readyChecker{})})
	response = ut.PerformRequest(withoutAuth.Engine, consts.MethodPost, "/api/v1/me/articles/42/reads", nil)
	if response.Code != consts.StatusNotFound {
		t.Fatalf("认证关闭仍注册路由: %d", response.Code)
	}
}

func listIDs(n int) string {
	values := make([]string, n)
	for i := range values {
		values[i] = strconv.Itoa(i + 1)
	}
	return strings.Join(values, ",")
}
