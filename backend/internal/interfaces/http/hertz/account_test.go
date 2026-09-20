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
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/dto"
)

const testOrigin = "https://velis.example.com"

type fakeAccount struct {
	user             accountDomain.User
	loginResult      accountApp.LoginResult
	refreshResult    accountApp.RefreshResult
	identity         accountApp.Identity
	err              error
	lastLogin        accountApp.LoginInput
	lastRefreshToken string
	lastNickname     string
	lastRegister     accountApp.RegisterInput
}

func (f *fakeAccount) Register(_ context.Context, input accountApp.RegisterInput) (accountDomain.User, error) {
	f.lastRegister = input
	return f.user, f.err
}

func (f *fakeAccount) Login(_ context.Context, input accountApp.LoginInput) (accountApp.LoginResult, error) {
	f.lastLogin = input
	return f.loginResult, f.err
}

func (f *fakeAccount) Refresh(_ context.Context, raw string) (accountApp.RefreshResult, error) {
	f.lastRefreshToken = raw
	return f.refreshResult, f.err
}

func (f *fakeAccount) LogoutByRefreshToken(_ context.Context, raw string) error {
	f.lastRefreshToken = raw
	return f.err
}

func (f *fakeAccount) Authenticate(context.Context, string) (accountApp.Identity, error) {
	return f.identity, f.err
}

func (f *fakeAccount) UpdateNickname(_ context.Context, _ accountApp.Identity, nickname string) (accountDomain.User, error) {
	f.lastNickname = nickname
	return f.user, f.err
}

func testUser() accountDomain.User {
	return accountDomain.User{
		ID: accountDomain.UUID{1}, Username: "alice", Nickname: "alice",
		Role: accountDomain.RoleUser, Status: accountDomain.StatusActive,
		CreatedAt: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC),
	}
}

func newAuthServer(t *testing.T, account *fakeAccount, secure bool) *server.Hertz {
	t.Helper()
	return NewServer(Options{
		Address: "127.0.0.1:0", ShutdownTimeout: time.Second,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health: health.NewService(readyChecker{}),
		Auth: &AuthOptions{
			Service:        account,
			AllowedOrigin:  testOrigin,
			CookieSecure:   secure,
			TrustedProxies: []*net.IPNet{},
			Clock:          testClock{},
		},
	})
}

func postJSON(h *server.Hertz, path, body string, headers ...ut.Header) *ut.ResponseRecorder {
	base := []ut.Header{{Key: "Content-Type", Value: "application/json"}, {Key: "Origin", Value: testOrigin}}
	return ut.PerformRequest(h.Engine, consts.MethodPost, path, &ut.Body{Body: strings.NewReader(body), Len: len(body)}, append(base, headers...)...)
}

func setCookies(response *ut.ResponseRecorder) string {
	parts := make([]string, 0)
	for _, value := range response.Header().PeekAll("Set-Cookie") {
		if len(value) > 0 {
			parts = append(parts, string(value))
		}
	}
	return strings.Join(parts, "\n")
}

func TestRegisterEndpointContract(t *testing.T) {
	account := &fakeAccount{user: testUser()}
	h := newAuthServer(t, account, false)

	response := postJSON(h, "/api/v1/auth/register", `{"username":"Alice","password":"Abcd123!","nickname":"昵称"}`)
	if response.Code != consts.StatusCreated {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var body dto.AccountResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Username != "alice" || body.CreatedAt.Location() != time.UTC {
		t.Fatalf("响应 = %+v", body)
	}
	if account.lastRegister.Username != "Alice" || account.lastRegister.Nickname == nil || *account.lastRegister.Nickname != "昵称" {
		t.Fatalf("输入 = %+v", account.lastRegister)
	}
	// Hertz 的响应头表总带一个空的 Set-Cookie 槽位，这里只统计非空值。
	if setCookies(response) != "" {
		t.Fatalf("注册不得建立会话或写 Cookie: %s", setCookies(response))
	}
}

func TestLoginSetsCookiesAndReturnsAccessToken(t *testing.T) {
	account := &fakeAccount{loginResult: accountApp.LoginResult{
		User: testUser(),
		Session: accountDomain.NewSession(accountDomain.UUID{2}, accountDomain.UUID{1},
			time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC), time.Hour),
		AccessToken: "access-token", AccessExpiresAt: time.Date(2026, 9, 19, 12, 15, 0, 0, time.UTC),
		RefreshToken: "refresh-token",
	}}
	h := newAuthServer(t, account, false)
	response := postJSON(h, "/api/v1/auth/login", `{"username":"alice","password":"Abcd123!"}`)
	if response.Code != consts.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var body dto.SessionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.AccessToken != "access-token" || !strings.Contains(response.Body.String(), `"username":"alice"`) {
		t.Fatalf("响应 = %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "refresh-token") {
		t.Fatal("刷新令牌不得出现在响应正文")
	}
	cookies := setCookies(response)
	lower := strings.ToLower(cookies)
	if !strings.Contains(cookies, "velis_refresh=refresh-token") || !strings.Contains(lower, "httponly") ||
		!strings.Contains(lower, "samesite=strict") || !strings.Contains(lower, "path=/") {
		t.Fatalf("刷新 Cookie 属性 = %s", cookies)
	}
	if !strings.Contains(cookies, "velis_csrf=") {
		t.Fatalf("应写入 CSRF Cookie: %s", cookies)
	}
	if account.lastLogin.Username != "alice" || account.lastLogin.Password != "Abcd123!" {
		t.Fatalf("登录输入 = %+v", account.lastLogin)
	}
}

func TestSecureCookieNamesUseHostPrefix(t *testing.T) {
	account := &fakeAccount{loginResult: accountApp.LoginResult{
		User: testUser(),
		Session: accountDomain.NewSession(accountDomain.UUID{2}, accountDomain.UUID{1},
			time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC), time.Hour),
		AccessToken: "access-token", AccessExpiresAt: time.Date(2026, 9, 19, 12, 15, 0, 0, time.UTC),
		RefreshToken: "refresh-token",
	}}
	h := newAuthServer(t, account, true)
	response := postJSON(h, "/api/v1/auth/login", `{"username":"alice","password":"Abcd123!"}`)
	cookies := setCookies(response)
	if !strings.Contains(cookies, "__Host-velis_refresh=") || !strings.Contains(cookies, "__Host-velis_csrf=") ||
		!strings.Contains(strings.ToLower(cookies), "secure") {
		t.Fatalf("生产 Cookie = %s", cookies)
	}
}

func TestOriginAndCSRFMatrix(t *testing.T) {
	account := &fakeAccount{refreshResult: accountApp.RefreshResult{
		User: testUser(),
		Session: accountDomain.NewSession(accountDomain.UUID{2}, accountDomain.UUID{1},
			time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC), time.Hour),
		AccessToken: "new-access", AccessExpiresAt: time.Date(2026, 9, 19, 12, 15, 0, 0, time.UTC),
		RefreshToken: "new-refresh",
	}}
	h := newAuthServer(t, account, false)
	loginBody := `{"username":"alice","password":"Abcd123!"}`

	cases := []struct {
		name     string
		headers  []ut.Header
		path     string
		body     string
		wantCode int
		wantBody string
	}{
		{name: "缺少来源", headers: []ut.Header{{Key: "Content-Type", Value: "application/json"}},
			path: "/api/v1/auth/login", body: loginBody, wantCode: consts.StatusForbidden, wantBody: `"code":"CSRF_REJECTED"`},
		{name: "来源不匹配", headers: []ut.Header{{Key: "Content-Type", Value: "application/json"}, {Key: "Origin", Value: "https://evil.example.com"}},
			path: "/api/v1/auth/login", body: loginBody, wantCode: consts.StatusForbidden, wantBody: `"code":"CSRF_REJECTED"`},
		{name: "跨站 Fetch Metadata", headers: []ut.Header{{Key: "Content-Type", Value: "application/json"}, {Key: "Origin", Value: testOrigin}, {Key: "Sec-Fetch-Site", Value: "cross-site"}},
			path: "/api/v1/auth/login", body: loginBody, wantCode: consts.StatusForbidden, wantBody: `"code":"CSRF_REJECTED"`},
		{name: "Referer 回退", headers: []ut.Header{{Key: "Content-Type", Value: "application/json"}, {Key: "Referer", Value: testOrigin + "/login"}},
			path: "/api/v1/auth/login", body: loginBody, wantCode: consts.StatusOK},
		{name: "刷新缺少 CSRF", headers: []ut.Header{{Key: "Content-Type", Value: "application/json"}, {Key: "Origin", Value: testOrigin}},
			path: "/api/v1/auth/refresh", body: `{}`, wantCode: consts.StatusForbidden, wantBody: `"code":"CSRF_REJECTED"`},
		{name: "刷新 CSRF 不匹配", headers: []ut.Header{{Key: "Content-Type", Value: "application/json"}, {Key: "Origin", Value: testOrigin}, {Key: "X-CSRF-Token", Value: "wrong"}},
			path: "/api/v1/auth/refresh", body: `{}`, wantCode: consts.StatusForbidden, wantBody: `"code":"CSRF_REJECTED"`},
		{name: "非 JSON 正文", headers: []ut.Header{{Key: "Content-Type", Value: "text/plain"}, {Key: "Origin", Value: testOrigin}},
			path: "/api/v1/auth/login", body: loginBody, wantCode: consts.StatusBadRequest, wantBody: `"code":"VALIDATION_FAILED"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := ut.PerformRequest(h.Engine, consts.MethodPost, tc.path,
				&ut.Body{Body: strings.NewReader(tc.body), Len: len(tc.body)}, tc.headers...)
			if response.Code != tc.wantCode {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
			if tc.wantBody != "" && !strings.Contains(response.Body.String(), tc.wantBody) {
				t.Fatalf("body = %s", response.Body.String())
			}
		})
	}

	// 携带匹配的 CSRF 值时可刷新，并轮换 Cookie。
	response := ut.PerformRequest(h.Engine, consts.MethodPost, "/api/v1/auth/refresh",
		&ut.Body{Body: strings.NewReader(`{}`), Len: 2},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "Origin", Value: testOrigin},
		ut.Header{Key: "X-CSRF-Token", Value: "csrf-value"},
		ut.Header{Key: "Cookie", Value: "velis_csrf=csrf-value; velis_refresh=old-token"},
	)
	if response.Code != consts.StatusOK || !strings.Contains(response.Body.String(), "new-access") {
		t.Fatalf("刷新失败: status=%d body=%s", response.Code, response.Body.String())
	}
	if account.lastRefreshToken != "old-token" {
		t.Fatalf("刷新应读取 Cookie 中的令牌: %q", account.lastRefreshToken)
	}
	if !strings.Contains(setCookies(response), "velis_refresh=new-refresh") {
		t.Fatal("刷新应轮换刷新 Cookie")
	}
}

func TestStrictJSONDecoding(t *testing.T) {
	h := newAuthServer(t, &fakeAccount{user: testUser()}, false)
	cases := map[string]string{
		"未知字段":   `{"username":"alice","password":"Abcd123!","extra":1}`,
		"重复字段":   `{"username":"alice","username":"bob","password":"Abcd123!"}`,
		"尾随内容":   `{"username":"alice","password":"Abcd123!"} {"more":1}`,
		"非对象":    `["alice"]`,
		"空正文":    ``,
		"正文超过上限": `{"username":"alice","password":"` + strings.Repeat("a", 8<<10) + `"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			response := postJSON(h, "/api/v1/auth/register", body)
			if response.Code != consts.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"VALIDATION_FAILED"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestProtectedEndpointsRequireBearer(t *testing.T) {
	account := &fakeAccount{identity: accountApp.Identity{
		User:    testUser(),
		Session: accountDomain.NewSession(accountDomain.UUID{2}, accountDomain.UUID{1}, time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC), time.Hour),
	}}
	h := newAuthServer(t, account, false)

	missing := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/account/me", nil)
	if missing.Code != consts.StatusUnauthorized || !strings.Contains(missing.Body.String(), `"code":"AUTH_SESSION_INVALID"`) {
		t.Fatalf("缺少令牌: status=%d body=%s", missing.Code, missing.Body.String())
	}

	authorized := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/account/me", nil,
		ut.Header{Key: "Authorization", Value: "Bearer access-token"})
	if authorized.Code != consts.StatusOK || !strings.Contains(authorized.Body.String(), `"username":"alice"`) {
		t.Fatalf("携带令牌: status=%d body=%s", authorized.Code, authorized.Body.String())
	}

	// 本人资源修改不要求来源校验，但必须是 JSON 且只接受 nickname。
	patched := ut.PerformRequest(h.Engine, consts.MethodPatch, "/api/v1/account/me",
		&ut.Body{Body: strings.NewReader(`{"nickname":"新昵称"}`), Len: 24},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "Authorization", Value: "Bearer access-token"})
	if patched.Code != consts.StatusOK || account.lastNickname != "新昵称" {
		t.Fatalf("修改昵称: status=%d body=%s", patched.Code, patched.Body.String())
	}

	unknownField := ut.PerformRequest(h.Engine, consts.MethodPatch, "/api/v1/account/me",
		&ut.Body{Body: strings.NewReader(`{"nickname":"x","role":"admin"}`), Len: 30},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "Authorization", Value: "Bearer access-token"})
	if unknownField.Code != consts.StatusBadRequest {
		t.Fatalf("不得允许修改角色: status=%d body=%s", unknownField.Code, unknownField.Body.String())
	}
}

func TestAccountErrorMapping(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantCode   int
		wantBody   string
		wantHeader string
	}{
		{"注册关闭", accountApp.ErrRegistrationDisabled, consts.StatusForbidden, `"code":"AUTH_REGISTRATION_DISABLED"`, ""},
		{"凭证错误", accountApp.ErrInvalidCredentials, consts.StatusUnauthorized, `"code":"AUTH_INVALID_CREDENTIALS"`, ""},
		{"用户名冲突", accountDomain.ErrUsernameTaken, consts.StatusConflict, `"code":"AUTH_USERNAME_CONFLICT"`, ""},
		{"昵称非法", accountDomain.ErrInvalidNickname, consts.StatusBadRequest, `"code":"VALIDATION_FAILED"`, ""},
		{"散列繁忙", ports.ErrHasherBusy, consts.StatusServiceUnavailable, `"code":"AUTH_HASH_BUSY"`, "Retry-After"},
		{"限流", &accountApp.RateLimitedError{RetryAfter: 90 * time.Second}, consts.StatusTooManyRequests, `"code":"AUTH_RATE_LIMITED"`, "Retry-After"},
		{"依赖故障", io.ErrUnexpectedEOF, consts.StatusServiceUnavailable, `"code":"DEPENDENCY_UNAVAILABLE"`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newAuthServer(t, &fakeAccount{err: tc.err}, false)
			response := postJSON(h, "/api/v1/auth/login", `{"username":"alice","password":"Abcd123!"}`)
			if response.Code != tc.wantCode || !strings.Contains(response.Body.String(), tc.wantBody) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if tc.wantHeader != "" && response.Header().Get(tc.wantHeader) == "" {
				t.Fatalf("缺少 %s 头", tc.wantHeader)
			}
		})
	}
}

func TestLogoutClearsCookiesAndIsIdempotent(t *testing.T) {
	account := &fakeAccount{}
	h := newAuthServer(t, account, false)
	response := ut.PerformRequest(h.Engine, consts.MethodPost, "/api/v1/auth/logout",
		&ut.Body{Body: strings.NewReader(`{}`), Len: 2},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "Origin", Value: testOrigin},
		ut.Header{Key: "X-CSRF-Token", Value: "csrf-value"},
		ut.Header{Key: "Cookie", Value: "velis_csrf=csrf-value; velis_refresh=old-token"},
	)
	if response.Code != consts.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	cookies := setCookies(response)
	if !strings.Contains(cookies, "velis_refresh=;") || !strings.Contains(strings.ToLower(cookies), "max-age=0") {
		t.Fatalf("退出应清除 Cookie: %s", cookies)
	}
	if account.lastRefreshToken != "old-token" {
		t.Fatalf("退出应读取刷新 Cookie: %q", account.lastRefreshToken)
	}
}
