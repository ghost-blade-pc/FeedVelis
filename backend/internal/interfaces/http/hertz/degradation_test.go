package hertz

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	assetApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asset"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/handler"
)

// TestDegradedAssetsKeepContentPathsUsable 固化 6.7 的降级矩阵：
// 资产端点整体 503，但内容主链路与就绪检查完全不受影响。
func TestDegradedAssetsKeepContentPathsUsable(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	articles := articleApp.NewService(&listRepository{}, noOpSanitizer{}, testClock{})
	account := &fakeAccount{identity: testIdentity()}
	h := NewServer(Options{
		Address: "127.0.0.1:0", ShutdownTimeout: time.Second, Logger: logger,
		Health:   health.NewService(readyChecker{}),
		Articles: articles,
		// 未配置对象存储时的装配结果：资产端点存在但稳定降级。
		Assets: handler.UnavailableAssets{},
		Auth: &AuthOptions{
			Service: account, AllowedOrigin: testOrigin, Clock: testClock{},
			MyArticles: &fakeUserArticles{}, Assets: handler.UnavailableAssets{},
		},
	})

	// 图片字节读取：GET 与 HEAD 都返回 503 ASSET_UNAVAILABLE。
	for _, method := range []string{consts.MethodGet, consts.MethodHead} {
		response := ut.PerformRequest(h.Engine, method, "/api/v1/assets/"+testAssetID+"/content", nil)
		if response.Code != consts.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"ASSET_UNAVAILABLE"`) {
			t.Fatalf("%s 图片端点 status=%d body=%s", method, response.Code, response.Body.String())
		}
		if retryAfter := response.Header().Get("Retry-After"); retryAfter == "" {
			t.Fatalf("%s 降级响应应带 Retry-After", method)
		}
	}

	// 资产写入同样降级，而不是 404。
	create := myRequest(h, consts.MethodPost, "/api/v1/me/assets", `{"content_type":"image/png","size_bytes":1}`,
		ut.Header{Key: "Idempotency-Key", Value: validKey})
	if create.Code != consts.StatusServiceUnavailable || !strings.Contains(create.Body.String(), `"code":"ASSET_UNAVAILABLE"`) {
		t.Fatalf("创建资产 status=%d body=%s", create.Code, create.Body.String())
	}
	confirm := myRequest(h, consts.MethodPost, "/api/v1/me/assets/"+testAssetID+"/confirm", "",
		ut.Header{Key: "Idempotency-Key", Value: validKey})
	if confirm.Code != consts.StatusServiceUnavailable {
		t.Fatalf("确认资产 status=%d body=%s", confirm.Code, confirm.Body.String())
	}

	// 内容主链路与就绪检查保持可用：MinIO 不是核心依赖。
	for _, path := range []string{"/livez", "/readyz", "/api/v1/articles"} {
		if response := ut.PerformRequest(h.Engine, consts.MethodGet, path, nil); response.Code != consts.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	// 纯文本投稿不把对象存储当事务硬依赖。
	published := myRequest(h, consts.MethodPost, "/api/v1/me/articles",
		`{"title":"纯文本","markdown":"正文","initial_status":"published"}`,
		ut.Header{Key: "Idempotency-Key", Value: validKey})
	if published.Code != consts.StatusCreated {
		t.Fatalf("纯文本投稿 status=%d body=%s", published.Code, published.Body.String())
	}
}

// TestUnavailableAssetsImplementsServiceContract 保证降级实现与真实服务同形。
func TestUnavailableAssetsImplementsServiceContract(t *testing.T) {
	var service handler.AssetService = handler.UnavailableAssets{}
	ctx := context.Background()
	// 四个入口一致降级：任何一条返回 nil 错误都会让客户端误以为能力可用。
	if _, _, err := service.Create(ctx, assetApp.CreateCommand{}); !errors.Is(err, ports.ErrAssetStorageUnavailable) {
		t.Fatalf("Create err = %v", err)
	}
	if _, _, err := service.Confirm(ctx, assetApp.ConfirmCommand{}); !errors.Is(err, ports.ErrAssetStorageUnavailable) {
		t.Fatalf("Confirm err = %v", err)
	}
	if _, err := service.Open(ctx, assetApp.ContentCommand{}); !errors.Is(err, ports.ErrAssetStorageUnavailable) {
		t.Fatalf("Open err = %v", err)
	}
	if _, err := service.Stat(ctx, assetApp.ContentCommand{}); !errors.Is(err, ports.ErrAssetStorageUnavailable) {
		t.Fatalf("Stat err = %v", err)
	}
}

func TestReadyzIsIndependentOfAssetStorage(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// readyz 只探测核心依赖：即便资产降级，PostgreSQL 正常时就绪检查仍应通过。
	h := NewServer(Options{Address: "127.0.0.1:0", ShutdownTimeout: time.Second, Logger: logger,
		Health: health.NewService(readyChecker{}), Assets: handler.UnavailableAssets{}})
	if response := ut.PerformRequest(h.Engine, consts.MethodGet, "/readyz", nil); response.Code != consts.StatusOK {
		t.Fatalf("readyz status=%d body=%s", response.Code, response.Body.String())
	}
}
