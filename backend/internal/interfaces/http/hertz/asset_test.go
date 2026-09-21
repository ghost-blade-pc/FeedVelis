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
	assetApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asset"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	assetDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/asset"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/dto"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/handler"
)

const testAssetID = "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c11"

// fakeAssetService 按用例结果与授权结果驱动处理器。
type fakeAssetService struct {
	upload     assetApp.Upload
	asset      assetDomain.Asset
	content    assetApp.Content
	info       ports.ObjectInfo
	openErr    error
	statErr    error
	lastCreate assetApp.CreateCommand
	lastRead   assetApp.ContentCommand
	openCalls  int
}

func (f *fakeAssetService) Create(_ context.Context, command assetApp.CreateCommand) (assetApp.Upload, bool, error) {
	f.lastCreate = command
	return f.upload, false, f.openErr
}

func (f *fakeAssetService) Confirm(context.Context, assetApp.ConfirmCommand) (assetDomain.Asset, bool, error) {
	return f.asset, false, f.openErr
}

func (f *fakeAssetService) Open(_ context.Context, command assetApp.ContentCommand) (assetApp.Content, error) {
	f.lastRead, f.openCalls = command, f.openCalls+1
	if f.openErr != nil {
		return assetApp.Content{}, f.openErr
	}
	return f.content, nil
}

func (f *fakeAssetService) Stat(_ context.Context, command assetApp.ContentCommand) (ports.ObjectInfo, error) {
	f.lastRead = command
	if f.statErr != nil {
		return ports.ObjectInfo{}, f.statErr
	}
	return f.info, nil
}

func readyAsset(t *testing.T) assetDomain.Asset {
	t.Helper()
	pending, err := assetDomain.NewPending(testAssetID, testUser().ID.String(), time.Date(2026, 9, 21, 6, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	media, err := assetDomain.NewMedia(assetDomain.ContentTypePNG, 2048, 640, 480, "etag-object", assetDomain.Limits{
		MaxFileBytes: 10 << 20, MaxWidth: 8192, MaxHeight: 8192, MaxPixels: 40000000})
	if err != nil {
		t.Fatal(err)
	}
	if err := pending.Confirm(testUser().ID.String(), media, time.Date(2026, 9, 21, 6, 1, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	return pending
}

// tokenAwareAccount 只接受指定令牌，用于验证可选认证在令牌失效时回退为匿名。
type tokenAwareAccount struct {
	*fakeAccount
	validToken string
}

func (a tokenAwareAccount) Authenticate(_ context.Context, token string) (accountApp.Identity, error) {
	if token != a.validToken {
		return accountApp.Identity{}, accountDomain.ErrSessionInvalid
	}
	return a.fakeAccount.identity, nil
}

func testIdentity() accountApp.Identity {
	return accountApp.Identity{
		User:    testUser(),
		Session: accountDomain.NewSession(accountDomain.UUID{2}, accountDomain.UUID{1}, time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC), time.Hour),
	}
}

func newAssetServer(t *testing.T, assets *fakeAssetService) *server.Hertz {
	t.Helper()
	return newAssetServerWithAccount(t, assets, &fakeAccount{identity: testIdentity()})
}

func newAssetServerWithAccount(t *testing.T, assets *fakeAssetService, account handler.AccountService) *server.Hertz {
	t.Helper()
	return NewServer(Options{
		Address: "127.0.0.1:0", ShutdownTimeout: time.Second,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health: health.NewService(readyChecker{}),
		Assets: assets,
		Auth: &AuthOptions{
			Service: account, AllowedOrigin: testOrigin, TrustedProxies: []*net.IPNet{}, Clock: testClock{}, Assets: assets,
		},
	})
}

func TestCreateAssetReturnsPresignedUpload(t *testing.T) {
	expires := time.Date(2026, 9, 21, 6, 15, 0, 0, time.UTC)
	assets := &fakeAssetService{upload: assetApp.Upload{
		Asset: readyAsset(t), URL: "https://storage.example/article-assets/x?signature=abc",
		Method: "PUT", Headers: map[string]string{}, ExpiresAt: expires,
	}}
	h := newAssetServer(t, assets)
	response := myRequest(h, consts.MethodPost, "/api/v1/me/assets", `{"content_type":"image/png","size_bytes":2048}`,
		ut.Header{Key: "Idempotency-Key", Value: validKey})
	if response.Code != consts.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var upload dto.AssetUpload
	if err := json.Unmarshal(response.Body.Bytes(), &upload); err != nil {
		t.Fatal(err)
	}
	if upload.UploadMethod != "PUT" || upload.UploadURL == "" || !upload.ExpiresAt.Equal(expires) ||
		upload.Asset.ID != testAssetID || upload.Asset.Status != "ready" {
		t.Fatalf("响应 = %+v", upload)
	}
	if upload.Asset.ContentType == nil || *upload.Asset.ContentType != "image/png" || *upload.Asset.Width != 640 {
		t.Fatalf("资产 = %+v", upload.Asset)
	}
	if assets.lastCreate.OwnerUserID != testUser().ID.String() || assets.lastCreate.SizeBytes != 2048 {
		t.Fatalf("命令 = %+v", assets.lastCreate)
	}
}

func TestCreateAssetRejectsBadRequests(t *testing.T) {
	assets := &fakeAssetService{upload: assetApp.Upload{Asset: readyAsset(t), Method: "PUT"}}
	cases := map[string]struct {
		body    string
		headers []ut.Header
		code    int
		body2   string
	}{
		"缺少幂等键": {`{"content_type":"image/png","size_bytes":1}`, nil, consts.StatusBadRequest, `"code":"IDEMPOTENCY_KEY_REQUIRED"`},
		"未知字段": {`{"content_type":"image/png","size_bytes":1,"extra":1}`,
			[]ut.Header{{Key: "Idempotency-Key", Value: validKey}}, consts.StatusBadRequest, `"code":"VALIDATION_FAILED"`},
		"正文超限": {`{"content_type":"image/png","size_bytes":1,"padding":"` + strings.Repeat("a", 2<<10) + `"}`,
			[]ut.Header{{Key: "Idempotency-Key", Value: validKey}}, consts.StatusBadRequest, `"code":"VALIDATION_FAILED"`},
		"配额不足": {`{"content_type":"image/png","size_bytes":1}`,
			[]ut.Header{{Key: "Idempotency-Key", Value: validKey}}, consts.StatusConflict, `"code":"ASSET_QUOTA_EXCEEDED"`},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			service := &fakeAssetService{upload: assets.upload}
			if name == "配额不足" {
				service.openErr = assetApp.ErrQuotaExceeded
			}
			h := newAssetServer(t, service)
			response := myRequest(h, consts.MethodPost, "/api/v1/me/assets", testCase.body, testCase.headers...)
			if response.Code != testCase.code || !strings.Contains(response.Body.String(), testCase.body2) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestConfirmAssetReturnsConfirmedMetadata(t *testing.T) {
	assets := &fakeAssetService{asset: readyAsset(t)}
	h := newAssetServer(t, assets)
	response := myRequest(h, consts.MethodPost, "/api/v1/me/assets/"+testAssetID+"/confirm", "",
		ut.Header{Key: "Idempotency-Key", Value: validKey})
	if response.Code != consts.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var asset dto.ArticleAsset
	if err := json.Unmarshal(response.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	if asset.Status != "ready" || asset.SizeBytes == nil || *asset.SizeBytes != 2048 || asset.ConfirmedAt == nil {
		t.Fatalf("资产 = %+v", asset)
	}

	// 非法资产 ID 在进入用例前就被拒绝。
	invalid := myRequest(h, consts.MethodPost, "/api/v1/me/assets/not-a-uuid/confirm", "",
		ut.Header{Key: "Idempotency-Key", Value: validKey})
	if invalid.Code != consts.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"code":"VALIDATION_FAILED"`) {
		t.Fatalf("status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestAssetContentStreamsBytesWithSafetyHeaders(t *testing.T) {
	body := "<png-bytes>"
	assets := &fakeAssetService{content: assetApp.Content{
		Body: io.NopCloser(strings.NewReader(body)), SizeBytes: int64(len(body)),
		ContentType: assetDomain.ContentTypePNG, ETag: "etag-object",
	}}
	h := newAssetServer(t, assets)
	response := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/assets/"+testAssetID+"/content", nil)
	if response.Code != consts.StatusOK || response.Body.String() != body {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if got := string(response.Header().Peek("Content-Type")); got != "image/png" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := string(response.Header().Peek("ETag")); got != `"etag-object"` {
		t.Fatalf("ETag = %q", got)
	}
	if got := string(response.Header().Peek("X-Content-Type-Options")); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := string(response.Header().Peek("Content-Disposition")); got != "inline" {
		t.Fatalf("Content-Disposition = %q", got)
	}
	// 匿名读取不携带身份。
	if assets.lastRead.ViewerUserID != "" {
		t.Fatalf("匿名查看者 = %q", assets.lastRead.ViewerUserID)
	}
}

func TestAssetContentHeadReturnsMetadataWithoutBody(t *testing.T) {
	assets := &fakeAssetService{info: ports.ObjectInfo{SizeBytes: 4096, ContentType: assetDomain.ContentTypeWebP, ETag: "etag-head"}}
	h := newAssetServer(t, assets)
	response := ut.PerformRequest(h.Engine, consts.MethodHead, "/api/v1/assets/"+testAssetID+"/content", nil)
	if response.Code != consts.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	if len(response.Body.Bytes()) != 0 {
		t.Fatalf("HEAD 不得返回正文: %q", response.Body.String())
	}
	if got := string(response.Header().Peek("Content-Type")); got != "image/webp" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := response.Header().Get("Content-Length"); got != "4096" {
		t.Fatalf("Content-Length = %q", got)
	}
	if got := string(response.Header().Peek("ETag")); got != `"etag-head"` {
		t.Fatalf("ETag = %q", got)
	}
}

func TestAssetContentAuthorizesOwnerPreviewAndAnonymousReference(t *testing.T) {
	assets := &fakeAssetService{content: assetApp.Content{Body: io.NopCloser(strings.NewReader("x")), SizeBytes: 1}}
	h := newAssetServer(t, assets)

	// 作者预览草稿图片：携带有效身份时用例收到作者 ID。
	authorized := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/assets/"+testAssetID+"/content", nil,
		ut.Header{Key: "Authorization", Value: "Bearer access-token"})
	if authorized.Code != consts.StatusOK || assets.lastRead.ViewerUserID != testUser().ID.String() {
		t.Fatalf("作者预览 status=%d viewer=%q", authorized.Code, assets.lastRead.ViewerUserID)
	}

	// 令牌失效时按匿名继续，不用 401 暴露资源是否存在。
	strict := newAssetServerWithAccount(t, assets, tokenAwareAccount{fakeAccount: &fakeAccount{identity: testIdentity()}, validToken: "access-token"})
	anonymous := ut.PerformRequest(strict.Engine, consts.MethodGet, "/api/v1/assets/"+testAssetID+"/content", nil,
		ut.Header{Key: "Authorization", Value: "Bearer expired"})
	if anonymous.Code != consts.StatusOK || assets.lastRead.ViewerUserID != "" {
		t.Fatalf("失效令牌 status=%d viewer=%q", anonymous.Code, assets.lastRead.ViewerUserID)
	}
}

func TestAssetContentErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
		wantBody string
	}{
		{"下架即时拒绝", assetDomain.ErrNotFound, consts.StatusNotFound, `"code":"NOT_FOUND"`},
		{"存储故障降级", ports.ErrAssetStorageUnavailable, consts.StatusServiceUnavailable, `"code":"ASSET_UNAVAILABLE"`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newAssetServer(t, &fakeAssetService{openErr: testCase.err, statErr: testCase.err})
			for _, method := range []string{consts.MethodGet, consts.MethodHead} {
				response := ut.PerformRequest(h.Engine, method, "/api/v1/assets/"+testAssetID+"/content", nil)
				if response.Code != testCase.wantCode || !strings.Contains(response.Body.String(), testCase.wantBody) {
					t.Fatalf("%s status=%d body=%s", method, response.Code, response.Body.String())
				}
			}
		})
	}
}

// TestAssetContentRouteExistsWithoutAuth 固化装配语义：认证关闭时图片读取仍然可用。
func TestAssetContentRouteExistsWithoutAuth(t *testing.T) {
	assets := &fakeAssetService{content: assetApp.Content{Body: io.NopCloser(strings.NewReader("x")), SizeBytes: 1}}
	h := NewServer(Options{Address: "127.0.0.1:0", ShutdownTimeout: time.Second,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Health: health.NewService(readyChecker{}), Assets: assets})

	if response := ut.PerformRequest(h.Engine, consts.MethodGet, "/api/v1/assets/"+testAssetID+"/content", nil); response.Code != consts.StatusOK {
		t.Fatalf("匿名读取 status=%d body=%s", response.Code, response.Body.String())
	}
	// 写路由整体不注册。
	create := ut.PerformRequest(h.Engine, consts.MethodPost, "/api/v1/me/assets", nil,
		ut.Header{Key: "Content-Type", Value: "application/json"})
	if create.Code != consts.StatusNotFound {
		t.Fatalf("认证关闭时写路由 status=%d body=%s", create.Code, create.Body.String())
	}
}

func TestAssetEndpointsRequireIdentity(t *testing.T) {
	assets := &fakeAssetService{asset: readyAsset(t)}
	h := newAssetServer(t, assets)
	response := ut.PerformRequest(h.Engine, consts.MethodPost, "/api/v1/me/assets/"+testAssetID+"/confirm", nil,
		ut.Header{Key: "Idempotency-Key", Value: validKey})
	if response.Code != consts.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !errors.Is(assets.openErr, nil) && assets.openCalls != 0 {
		t.Fatal("未认证请求不得进入用例")
	}
}
