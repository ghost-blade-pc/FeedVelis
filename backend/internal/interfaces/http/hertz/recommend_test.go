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
	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type recommendServiceFake struct {
	page   recommendation.Page
	err    error
	userID string
	limit  int
	cursor string
	calls  int
}

func (f *recommendServiceFake) Get(_ context.Context, userID string, limit int, cursor string) (recommendation.Page, error) {
	f.calls++
	f.userID, f.limit, f.cursor = userID, limit, cursor
	return f.page, f.err
}

func recommendHTTPServer(fake *recommendServiceFake, account *fakeAccount) *server.Hertz {
	options := Options{Address: "127.0.0.1:0", ShutdownTimeout: time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Health: health.NewService(readyChecker{}), Recommend: fake}
	if account != nil {
		options.Auth = &AuthOptions{Service: account}
	}
	return NewServer(options)
}

func TestRecommendRouteAnonymousAuthenticatedAndParameters(t *testing.T) {
	next := "opaque"
	fake := &recommendServiceFake{page: recommendation.Page{Mode: "cold_start", Items: []recommendation.Item{{Article: articleDomain.ListItem{ID: 7, Title: "标题", SortAt: time.Now().UTC()}, Reason: "recent"}}, NextCursor: &next, HasMore: true}}
	h := recommendHTTPServer(fake, nil)
	response := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/articles/recommend?limit=2", nil)
	if response.Code != 200 || fake.userID != "" || fake.limit != 2 || !strings.Contains(response.Body.String(), `"recommendation_reason":"recent"`) || !strings.Contains(response.Body.String(), `"mode":"cold_start"`) {
		t.Fatalf("匿名推荐契约错误: %d %s", response.Code, response.Body.String())
	}
	for _, path := range []string{"?limit=0", "?limit=51", "?limit=x", "?limit=1&limit=2", "?x=1"} {
		response = ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/articles/recommend"+path, nil)
		if response.Code != 400 || !strings.Contains(response.Body.String(), `"code":"VALIDATION_FAILED"`) {
			t.Fatalf("参数 %s 未拒绝: %d %s", path, response.Code, response.Body.String())
		}
	}
	response = ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/articles/recommend?cursor=", nil)
	if response.Code != 400 || !strings.Contains(response.Body.String(), `"code":"INVALID_CURSOR"`) {
		t.Fatalf("空游标未拒绝: %d %s", response.Code, response.Body.String())
	}
	account := &fakeAccount{identity: accountApp.Identity{User: testUser()}}
	h = recommendHTTPServer(fake, account)
	response = ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/articles/recommend", nil, ut.Header{Key: "Authorization", Value: "Bearer valid"})
	if response.Code != 200 || fake.userID != testUser().ID.String() {
		t.Fatalf("有效身份未使用: %d %q", response.Code, fake.userID)
	}
	account.err = accountApp.ErrInvalidCredentials
	response = ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/articles/recommend", nil, ut.Header{Key: "Authorization", Value: "Bearer expired"})
	if response.Code != 401 || !strings.Contains(response.Body.String(), `"code":"AUTH_INVALID_CREDENTIALS"`) {
		t.Fatalf("失效凭证被降为匿名: %d %s", response.Code, response.Body.String())
	}
}

func TestRecommendRouteMapsCursorAndDependencyErrors(t *testing.T) {
	fake := &recommendServiceFake{err: recommendation.ErrInvalidCursor}
	h := recommendHTTPServer(fake, nil)
	response := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/articles/recommend?cursor=bad", nil)
	if response.Code != 400 || !strings.Contains(response.Body.String(), `"code":"INVALID_CURSOR"`) {
		t.Fatalf("无效游标映射错误: %d %s", response.Code, response.Body.String())
	}
	fake.err = recommendation.ErrDependencyUnavailable
	response = ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/articles/recommend", nil)
	if response.Code != 503 || !strings.Contains(response.Body.String(), `"code":"DEPENDENCY_UNAVAILABLE"`) {
		t.Fatalf("依赖错误映射错误: %d %s", response.Code, response.Body.String())
	}
}
