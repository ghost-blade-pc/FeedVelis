package bootstrap

import (
	"log/slog"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	assetApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asset"
	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	assetDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/asset"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/clock"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/content/markdown"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/objectstore/minio"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/handler"
	"github.com/jackc/pgx/v5/pgxpool"
)

// contentServices 承载 I2 内容写入用例：本人文章、管理员文章与资产共享同一幂等服务。
type contentServices struct {
	myArticles    *articleApp.UserService
	adminArticles *articleApp.AdminService
	// assets 始终非空：未配置或无法初始化对象存储时使用降级实现，
	// 使资产端点稳定返回 503 而不是从路由表里消失。
	assets handler.AssetService
	// store 供启动自检与 Worker 清理使用；资产降级时为 nil。
	store *minio.Store
	// idempotency 供其他模块的管理用例复用同一份幂等实现。
	idempotency *idempotencyApp.Service
}

// buildContentServices 装配内容写入链路；幂等保留期来自配置，默认 24 小时。
// 对象存储是局部可降级依赖：装配失败只让资产能力降级，内容主链路照常启动。
func buildContentServices(cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) contentServices {
	articleRepository := postgres.NewArticleRepository(pool)
	assetRepository := postgres.NewArticleAssetRepository(pool)
	txManager := postgres.NewTxManager(pool)
	clockValue := clock.System{}
	idempotency := idempotencyApp.NewService(
		postgres.NewIdempotencyRepository(pool), txManager, cfg.Idempotency.Retention)
	services := contentServices{
		idempotency: idempotency,
		myArticles: articleApp.NewUserService(articleRepository, markdown.NewUserRenderer(), assetRepository,
			idempotency, clockValue, articleApp.AssetPolicy{
				MaxImages:     cfg.Assets.ArticleImageLimit,
				MaxTotalBytes: cfg.Assets.ArticleBytesLimit,
			}),
		adminArticles: articleApp.NewAdminService(articleRepository, idempotency, clockValue),
	}
	store, err := buildAssetStore(cfg)
	if err != nil {
		// 配置了却初始化失败同样降级，而不是让整个 API 起不来。
		logger.Warn("资产存储不可用，图片能力降级", "error", err)
		services.assets = handler.UnavailableAssets{}
		return services
	}
	if store == nil {
		logger.Info("未配置资产存储，图片能力降级")
		services.assets = handler.UnavailableAssets{}
		return services
	}
	services.store = store
	services.assets = assetApp.NewService(assetRepository, store, idempotency, clockValue,
		assetApp.Policy{
			Limits: assetDomain.Limits{
				MaxFileBytes: cfg.Assets.MaxFileBytes, MaxWidth: cfg.Assets.MaxWidth,
				MaxHeight: cfg.Assets.MaxHeight, MaxPixels: cfg.Assets.MaxPixels,
			},
			UserQuotaBytes: cfg.Assets.UserQuotaBytes, PendingLimit: cfg.Assets.PendingLimit,
			UploadTTL: cfg.Assets.UploadTTL, ProbeBytes: assetProbeBytes,
		})
	return services
}

// buildAssetStore 在配置了对象存储时构造适配器；未配置返回 (nil, nil)。
func buildAssetStore(cfg config.Config) (*minio.Store, error) {
	if !cfg.Assets.Enabled() {
		return nil, nil
	}
	return minio.NewStore(minio.StoreConfig{
		Endpoint: cfg.Assets.Endpoint, AccessKey: cfg.Assets.AccessKey, SecretKey: cfg.Assets.SecretKey,
		UseTLS: cfg.Assets.UseTLS, Bucket: cfg.Assets.Bucket,
	})
}

// assetProbeBytes 是识别图片签名与尺寸允许读取的最大字节数：
// 三种受支持格式的头部都远小于它，超过就无法在预算内确认。
const assetProbeBytes = 1 << 20
