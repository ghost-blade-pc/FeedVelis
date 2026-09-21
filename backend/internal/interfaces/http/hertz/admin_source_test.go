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
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	sourceApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/source"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/dto"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/handler"
)

// fakeAdminSources 按用例结果驱动处理器。
type fakeAdminSources struct {
	source      sourceDomain.Source
	created     bool
	run         sourceDomain.FetchRun
	page        sourceApp.HistoryPage
	createErr   error
	mutateErr   error
	fetchErr    error
	lastCreate  sourceApp.CreateCommand
	lastSource  sourceApp.SourceCommand
	lastFetch   sourceApp.FetchCommand
	lastHistory sourceApp.HistoryCommand
	mutations   int
}

func (f *fakeAdminSources) Create(_ context.Context, command sourceApp.CreateCommand) (sourceApp.CreateResult, bool, error) {
	f.lastCreate = command
	return sourceApp.CreateResult{Source: f.source, Created: f.created}, false, f.createErr
}

func (f *fakeAdminSources) List(context.Context) ([]sourceDomain.Source, error) {
	return []sourceDomain.Source{f.source}, f.createErr
}

func (f *fakeAdminSources) Get(context.Context, int64) (sourceDomain.Source, error) {
	return f.source, f.createErr
}

func (f *fakeAdminSources) Pause(_ context.Context, command sourceApp.SourceCommand) (sourceDomain.Source, bool, error) {
	f.lastSource, f.mutations = command, f.mutations+1
	return f.source, false, f.mutateErr
}

func (f *fakeAdminSources) Resume(_ context.Context, command sourceApp.SourceCommand) (sourceDomain.Source, bool, error) {
	f.lastSource, f.mutations = command, f.mutations+1
	return f.source, false, f.mutateErr
}

func (f *fakeAdminSources) SetInterval(_ context.Context, command sourceApp.SourceCommand) (sourceDomain.Source, bool, error) {
	f.lastSource, f.mutations = command, f.mutations+1
	return f.source, false, f.mutateErr
}

func (f *fakeAdminSources) FetchNow(_ context.Context, command sourceApp.FetchCommand) (sourceApp.FetchRunResult, error) {
	f.lastFetch = command
	return sourceApp.FetchRunResult{Run: f.run}, f.fetchErr
}

func (f *fakeAdminSources) History(_ context.Context, command sourceApp.HistoryCommand) (sourceApp.HistoryPage, error) {
	f.lastHistory = command
	return f.page, f.createErr
}

func testSource() sourceDomain.Source {
	return sourceDomain.Source{
		ID: 7, FeedURL: "https://example.com/feed", Title: "Example", Status: sourceDomain.StatusActive,
		FetchInterval: 30 * time.Minute, LockVersion: 3,
		NextFetchAt: time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC),
		CreatedAt:   time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC),
	}
}

func newAdminSourceServer(t *testing.T, role accountDomain.Role, sources *fakeAdminSources) *server.Hertz {
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
			Service: account, AllowedOrigin: testOrigin, TrustedProxies: []*net.IPNet{},
			Clock: testClock{}, AdminSources: sources,
		},
	})
}

func adminSourceRequest(h *server.Hertz, method, path, body string, headers ...ut.Header) *ut.ResponseRecorder {
	base := []ut.Header{
		{Key: "Content-Type", Value: "application/json"},
		{Key: "Authorization", Value: "Bearer access-token"},
	}
	return ut.PerformRequest(h.Engine, method, path,
		&ut.Body{Body: strings.NewReader(body), Len: len(body)}, append(base, headers...)...)
}

func TestAdminSourceCreateContract(t *testing.T) {
	sources := &fakeAdminSources{source: testSource(), created: true}
	h := newAdminSourceServer(t, accountDomain.RoleAdmin, sources)
	response := adminSourceRequest(h, consts.MethodPost, "/api/v1/admin/sources",
		`{"feed_url":"https://example.com/feed","fetch_interval_seconds":3600}`,
		ut.Header{Key: "Idempotency-Key", Value: validKey})
	if response.Code != consts.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if etag := string(response.Header().Peek("ETag")); etag != `"3"` {
		t.Fatalf("ETag = %q", etag)
	}
	var body dto.AdminSource
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != 7 || body.FetchIntervalSeconds != 1800 || body.LockVersion != 3 || body.Status != "active" {
		t.Fatalf("响应 = %+v", body)
	}
	if sources.lastCreate.FetchInterval != time.Hour || sources.lastCreate.FeedURL != "https://example.com/feed" {
		t.Fatalf("命令 = %+v", sources.lastCreate)
	}
	// 契约 forbids 额外字段；响应不得泄露内部字段。
	if strings.Contains(response.Body.String(), "lease_") || strings.Contains(response.Body.String(), "normalized") {
		t.Fatalf("响应泄露内部字段: %s", response.Body.String())
	}

	// 未指定周期时交给用例取默认值。
	response = adminSourceRequest(h, consts.MethodPost, "/api/v1/admin/sources",
		`{"feed_url":"https://example.com/feed"}`,
		ut.Header{Key: "Idempotency-Key", Value: validKey})
	if response.Code != consts.StatusCreated || sources.lastCreate.FetchInterval != 0 {
		t.Fatalf("默认周期 status=%d command=%+v", response.Code, sources.lastCreate)
	}
}

func TestAdminSourceRejectsDuplicateAndBadRequests(t *testing.T) {
	cases := []struct {
		name     string
		sources  *fakeAdminSources
		body     string
		headers  []ut.Header
		wantCode int
		wantBody string
	}{
		{"重复 URL", &fakeAdminSources{source: testSource(), created: false},
			`{"feed_url":"https://example.com/feed"}`,
			[]ut.Header{{Key: "Idempotency-Key", Value: validKey}}, consts.StatusConflict, `"code":"SOURCE_ALREADY_EXISTS"`},
		{"缺少幂等键", &fakeAdminSources{source: testSource()},
			`{"feed_url":"https://example.com/feed"}`, nil, consts.StatusBadRequest, `"code":"IDEMPOTENCY_KEY_REQUIRED"`},
		{"未知字段", &fakeAdminSources{source: testSource()},
			`{"feed_url":"https://example.com/feed","extra":1}`,
			[]ut.Header{{Key: "Idempotency-Key", Value: validKey}}, consts.StatusBadRequest, `"code":"VALIDATION_FAILED"`},
		{"非法 URL", &fakeAdminSources{source: testSource(),
			createErr: sourceDomain.ErrInvalidURL},
			`{"feed_url":"file:///tmp/feed"}`,
			[]ut.Header{{Key: "Idempotency-Key", Value: validKey}}, consts.StatusBadRequest, `"code":"VALIDATION_FAILED"`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newAdminSourceServer(t, accountDomain.RoleAdmin, testCase.sources)
			response := adminSourceRequest(h, consts.MethodPost, "/api/v1/admin/sources", testCase.body, testCase.headers...)
			if response.Code != testCase.wantCode || !strings.Contains(response.Body.String(), testCase.wantBody) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAdminSourceWritesRequireIfMatch(t *testing.T) {
	sources := &fakeAdminSources{source: testSource()}
	h := newAdminSourceServer(t, accountDomain.RoleAdmin, sources)
	paths := []struct{ method, path, body string }{
		{consts.MethodPatch, "/api/v1/admin/sources/7", `{"fetch_interval_seconds":3600}`},
		{consts.MethodPost, "/api/v1/admin/sources/7/pause", ""},
		{consts.MethodPost, "/api/v1/admin/sources/7/resume", ""},
	}
	for _, item := range paths {
		response := adminSourceRequest(h, item.method, item.path, item.body,
			ut.Header{Key: "Idempotency-Key", Value: validKey})
		if response.Code != consts.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"IF_MATCH_REQUIRED"`) {
			t.Fatalf("%s %s status=%d body=%s", item.method, item.path, response.Code, response.Body.String())
		}
	}
	if sources.mutations != 0 {
		t.Fatal("缺少 If-Match 时不得进入用例")
	}

	// 携带 If-Match 时透传版本。
	response := adminSourceRequest(h, consts.MethodPost, "/api/v1/admin/sources/7/pause", "",
		ut.Header{Key: "Idempotency-Key", Value: validKey}, ut.Header{Key: "If-Match", Value: `"3"`})
	if response.Code != consts.StatusOK || sources.lastSource.ExpectedVersion != 3 {
		t.Fatalf("暂停 status=%d command=%+v", response.Code, sources.lastSource)
	}
	// 周期以秒传回用例。
	response = adminSourceRequest(h, consts.MethodPatch, "/api/v1/admin/sources/7", `{"fetch_interval_seconds":300}`,
		ut.Header{Key: "Idempotency-Key", Value: validKey}, ut.Header{Key: "If-Match", Value: `"3"`})
	if response.Code != consts.StatusOK || sources.lastSource.Interval != 5*time.Minute {
		t.Fatalf("改周期 status=%d command=%+v", response.Code, sources.lastSource)
	}
}

func TestAdminSourceFetchAndHistory(t *testing.T) {
	actor := testUser().ID.String()
	run := sourceDomain.Restore("6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c11", 7, sourceDomain.TriggerManual,
		&actor, sourceDomain.RunSucceeded, 1,
		sourceDomain.FetchRunStats{Inserted: 2, Skipped: 1}, nil,
		time.Date(2026, 9, 21, 11, 0, 0, 0, time.UTC), nil, time.Date(2026, 9, 21, 11, 0, 0, 0, time.UTC))
	sources := &fakeAdminSources{source: testSource(), run: run,
		page: sourceApp.HistoryPage{Items: []sourceDomain.FetchRun{run}, HasMore: true}}
	h := newAdminSourceServer(t, accountDomain.RoleAdmin, sources)

	fetched := adminSourceRequest(h, consts.MethodPost, "/api/v1/admin/sources/7/fetches", "",
		ut.Header{Key: "Idempotency-Key", Value: validKey})
	if fetched.Code != consts.StatusOK {
		t.Fatalf("触发抓取 status=%d body=%s", fetched.Code, fetched.Body.String())
	}
	var runBody dto.SourceFetchRun
	if err := json.Unmarshal(fetched.Body.Bytes(), &runBody); err != nil {
		t.Fatal(err)
	}
	if runBody.Status != "succeeded" || runBody.Trigger != "manual" || runBody.InsertedCount != 2 ||
		runBody.SkippedCount != 1 || runBody.SourceID != 7 {
		t.Fatalf("运行响应 = %+v", runBody)
	}
	// 历史不得回显操作者身份。
	if strings.Contains(fetched.Body.String(), actor) {
		t.Fatalf("运行响应泄露操作者标识: %s", fetched.Body.String())
	}
	if sources.lastFetch.ActorUserID != actor || sources.lastFetch.SourceID != 7 {
		t.Fatalf("抓取命令 = %+v", sources.lastFetch)
	}

	history := adminSourceRequest(h, consts.MethodGet, "/api/v1/admin/sources/7/fetches?limit=10&cursor=abc", "")
	if history.Code != consts.StatusOK || sources.lastHistory.Limit != 10 || sources.lastHistory.Cursor != "abc" {
		t.Fatalf("历史 status=%d command=%+v", history.Code, sources.lastHistory)
	}
	if !strings.Contains(history.Body.String(), `"has_more":true`) {
		t.Fatalf("历史正文 = %s", history.Body.String())
	}
	// 分页大小非法由用例拒绝。
	sources.createErr = sourceDomain.ErrInvalidPageSize
	invalid := adminSourceRequest(h, consts.MethodGet, "/api/v1/admin/sources/7/fetches?limit=101", "")
	if invalid.Code != consts.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"code":"VALIDATION_FAILED"`) {
		t.Fatalf("越界分页 status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	sources.createErr = nil

	// 非法来源 ID 在进入用例前被拒绝。
	badID := adminSourceRequest(h, consts.MethodGet, "/api/v1/admin/sources/abc/fetches", "")
	if badID.Code != consts.StatusBadRequest {
		t.Fatalf("非法来源 ID status=%d body=%s", badID.Code, badID.Body.String())
	}
}

func TestAdminSourceErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
		wantBody string
	}{
		{"版本冲突", sourceDomain.ErrVersionConflict, consts.StatusConflict, `"code":"SOURCE_VERSION_CONFLICT"`},
		{"租约被占用", sourceDomain.ErrLeaseHeld, consts.StatusConflict, `"code":"SOURCE_FETCH_IN_PROGRESS"`},
		{"来源不存在", sourceDomain.ErrNotFound, consts.StatusNotFound, `"code":"NOT_FOUND"`},
		{"状态不允许", sourceDomain.ErrInvalidStatus, consts.StatusConflict, `"code":"VALIDATION_FAILED"`},
		{"周期非法", sourceDomain.ErrInvalidInterval, consts.StatusBadRequest, `"code":"VALIDATION_FAILED"`},
		{"游标非法", sourceDomain.ErrInvalidRunCursor, consts.StatusBadRequest, `"code":"INVALID_CURSOR"`},
		{"抓取失败", sourceApp.NewFailureError("SSRF_BLOCKED", errors.New("受限地址")), consts.StatusConflict, `"code":"SOURCE_FETCH_FAILED"`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			sources := &fakeAdminSources{source: testSource(), mutateErr: testCase.err, fetchErr: testCase.err, createErr: testCase.err}
			h := newAdminSourceServer(t, accountDomain.RoleAdmin, sources)
			response := adminSourceRequest(h, consts.MethodPost, "/api/v1/admin/sources/7/pause", "",
				ut.Header{Key: "Idempotency-Key", Value: validKey}, ut.Header{Key: "If-Match", Value: `"3"`})
			if response.Code != testCase.wantCode || !strings.Contains(response.Body.String(), testCase.wantBody) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

// TestAdminSourceRoutesRejectRegularUser 固化「普通用户不得访问」。
func TestAdminSourceRoutesRejectRegularUser(t *testing.T) {
	sources := &fakeAdminSources{source: testSource(), created: true}
	h := newAdminSourceServer(t, accountDomain.RoleUser, sources)
	requests := []struct{ method, path, body string }{
		{consts.MethodGet, "/api/v1/admin/sources", ""},
		{consts.MethodPost, "/api/v1/admin/sources", `{"feed_url":"https://example.com/feed"}`},
		{consts.MethodGet, "/api/v1/admin/sources/7", ""},
		{consts.MethodPost, "/api/v1/admin/sources/7/fetches", ""},
	}
	for _, item := range requests {
		response := adminSourceRequest(h, item.method, item.path, item.body,
			ut.Header{Key: "Idempotency-Key", Value: validKey}, ut.Header{Key: "If-Match", Value: `"3"`})
		if response.Code != consts.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"FORBIDDEN"`) {
			t.Fatalf("%s %s status=%d body=%s", item.method, item.path, response.Code, response.Body.String())
		}
	}
	if sources.mutations != 0 || sources.lastCreate.FeedURL != "" {
		t.Fatal("普通用户不得进入管理员用例")
	}
	// 未认证请求得到 401 而不是 403。
	unauthenticated := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/admin/sources", nil)
	if unauthenticated.Code != consts.StatusUnauthorized {
		t.Fatalf("未认证 status=%d body=%s", unauthenticated.Code, unauthenticated.Body.String())
	}
}

// TestAdminSourceRoutesAbsentWithoutAuth 固化认证关闭时的装配语义。
func TestAdminSourceRoutesAbsentWithoutAuth(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewServer(Options{Address: "127.0.0.1:0", ShutdownTimeout: time.Second, Logger: logger,
		Health: health.NewService(readyChecker{})})
	response := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/admin/sources", nil)
	if response.Code != consts.StatusNotFound {
		t.Fatalf("认证关闭时管理员路由 status=%d body=%s", response.Code, response.Body.String())
	}
}

var _ handler.AdminSourceService = (*fakeAdminSources)(nil)
