// Package hertz 负责装配 HTTP 路由和协议级中间件。
package hertz

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/handler"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/middleware"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/presenter"
)

// Options 是 HTTP 服务的装配选项。
type Options struct {
	Address         string
	ShutdownTimeout time.Duration
	Logger          *slog.Logger
	Health          *health.Service
	Articles        *articleApp.Service
	// Assets 提供图片字节读取；为 nil 时不注册资产端点。匿名与作者共用同一路径。
	Assets handler.AssetService
	// Auth 为 nil 时不注册认证路由，保持原有匿名行为。
	Auth *AuthOptions
}

// AuthOptions 提供认证端点需要的装配；仅在 auth.enabled 为真时构造。
type AuthOptions struct {
	Service        handler.AccountService
	AllowedOrigin  string
	TrustedProxies []*net.IPNet
	CookieSecure   bool
	Clock          ports.Clock
	// MyArticles 为 nil 时不注册本人文章写路由；认证开启时才可能非空。
	MyArticles handler.UserArticleService
	// AdminArticles 为 nil 时不注册管理员文章路由；认证开启时才可能非空。
	AdminArticles handler.AdminArticleService
	// Assets 为 nil 时不注册本人资产写路由。
	Assets handler.AssetService
	// AdminSources 为 nil 时不注册管理员 Source 路由。
	AdminSources handler.AdminSourceService
}

func NewServer(options Options) *server.Hertz {
	h := server.New(
		server.WithHostPorts(options.Address),
		server.WithExitWaitTime(options.ShutdownTimeout),
		server.WithDisablePrintRoute(true),
	)

	// 固定顺序：Request ID → Recovery → Access Log。认证路由在其后按端点矩阵追加校验。
	h.Use(middleware.RequestID, middleware.Recovery(options.Logger), middleware.AccessLog(options.Logger))

	healthHandler := handler.NewHealth(options.Health)
	h.GET("/livez", healthHandler.Live)
	h.GET("/readyz", healthHandler.Ready)
	h.GET("/api/v1/ping", func(_ context.Context, c *app.RequestContext) {
		c.JSON(consts.StatusOK, map[string]string{"message": "pong"})
	})
	if options.Articles != nil {
		articleHandler := handler.NewArticle(options.Articles)
		h.GET("/api/v1/articles", articleHandler.List)
		h.GET("/api/v1/articles/:id", articleHandler.Get)
	}
	if options.Assets != nil {
		registerAssetContentRoutes(h, options)
	}
	if options.Auth != nil {
		registerAuthRoutes(h, options.Auth, options.Logger)
	}
	h.NoRoute(func(_ context.Context, c *app.RequestContext) {
		presenter.WriteNotFound(c, middleware.RequestIDFrom(c))
	})
	return h
}

func registerAuthRoutes(h *server.Hertz, options *AuthOptions, logger *slog.Logger) {
	cookies := handler.NewCookiePolicy(options.CookieSecure)
	guard := middleware.GuardPolicy{AllowedOrigin: options.AllowedOrigin, CSRFName: cookies.CSRFName}
	accountHandler := handler.NewAccount(options.Service, handler.AccountPolicy{
		Cookies:        cookies,
		TrustedProxies: options.TrustedProxies,
		Clock:          options.Clock,
		Logger:         logger,
	})

	// 端点校验矩阵：注册与登录校验来源；刷新与退出额外要求 CSRF；读取与本人资源不校验来源。
	auth := h.Group("/api/v1/auth")
	auth.POST("/register", middleware.RequireJSON(), middleware.RequireTrustedOrigin(guard), accountHandler.Register)
	auth.POST("/login", middleware.RequireJSON(), middleware.RequireTrustedOrigin(guard), accountHandler.Login)
	auth.POST("/refresh", middleware.RequireJSON(), middleware.RequireTrustedOrigin(guard), middleware.RequireCSRF(guard), accountHandler.Refresh)
	auth.POST("/logout", middleware.RequireJSON(), middleware.RequireTrustedOrigin(guard), middleware.RequireCSRF(guard), accountHandler.Logout)

	account := h.Group("/api/v1/account")
	account.GET("/me", middleware.Authenticate(options.Service), accountHandler.GetMe)
	account.PATCH("/me", middleware.RequireJSON(), middleware.Authenticate(options.Service), accountHandler.UpdateMe)

	if options.MyArticles != nil {
		registerMyArticleRoutes(h, options, logger)
	}
	if options.AdminArticles != nil {
		registerAdminArticleRoutes(h, options, logger)
	}
	if options.Assets != nil {
		registerMyAssetRoutes(h, options, logger)
	}
	if options.AdminSources != nil {
		registerAdminSourceRoutes(h, options, logger)
	}
}

// registerAdminSourceRoutes 装配管理员 Source 管理端点：
// 身份校验之后还要通过当前数据库角色检查，普通用户一律拒绝。
func registerAdminSourceRoutes(h *server.Hertz, options *AuthOptions, logger *slog.Logger) {
	sources := handler.NewAdminSource(options.AdminSources, logger)
	admin := middleware.RequireAdmin()
	group := h.Group("/api/v1/admin/sources")
	group.GET("", middleware.Authenticate(options.Service), admin, sources.List)
	group.POST("", middleware.RequireJSON(), middleware.Authenticate(options.Service), admin, sources.Create)
	group.GET("/:source_id", middleware.Authenticate(options.Service), admin, sources.Get)
	group.PATCH("/:source_id", middleware.RequireJSON(), middleware.Authenticate(options.Service), admin, sources.Update)
	group.POST("/:source_id/pause", middleware.Authenticate(options.Service), admin, sources.Pause)
	group.POST("/:source_id/resume", middleware.Authenticate(options.Service), admin, sources.Resume)
	group.GET("/:source_id/fetches", middleware.Authenticate(options.Service), admin, sources.History)
	group.POST("/:source_id/fetches", middleware.Authenticate(options.Service), admin, sources.Fetch)
}

// registerAssetContentRoutes 装配图片读取端点：匿名可访问，携带有效令牌时按作者身份预览。
func registerAssetContentRoutes(h *server.Hertz, options Options) {
	assets := handler.NewAsset(options.Assets, options.Logger)
	// 认证关闭时没有可用的身份解析器，直接按匿名继续。
	optional := func(c context.Context, ctx *app.RequestContext) { ctx.Next(c) }
	if options.Auth != nil {
		optional = middleware.AuthenticateOptional(options.Auth.Service)
	}
	content := h.Group("/api/v1/assets")
	content.GET("/:asset_id/content", optional, assets.ContentGet)
	content.HEAD("/:asset_id/content", optional, assets.ContentHead)
}

// registerMyAssetRoutes 装配本人资产写入端点：创建与确认都要求身份与幂等键。
func registerMyAssetRoutes(h *server.Hertz, options *AuthOptions, logger *slog.Logger) {
	assets := handler.NewAsset(options.Assets, logger)
	authenticated := middleware.Authenticate(options.Service)
	group := h.Group("/api/v1/assets")
	group.POST("", middleware.RequireJSON(), authenticated, assets.Create)
	group.POST("/:asset_id/confirm", authenticated, assets.Confirm)
}

// registerMyArticleRoutes 装配本人文章端点：读取只要求身份，写入额外要求 JSON 正文。
// 幂等键与 If-Match 在 Handler 内解析，这里只保证请求已认证且是 JSON。
func registerMyArticleRoutes(h *server.Hertz, options *AuthOptions, logger *slog.Logger) {
	myArticles := handler.NewUserArticle(options.MyArticles, logger)
	authenticated := middleware.Authenticate(options.Service)

	articles := h.Group("/api/v1/me/articles")
	articles.GET("", authenticated, myArticles.List)
	articles.POST("", middleware.RequireJSON(), authenticated, myArticles.Create)
	articles.POST("/preview", middleware.RequireJSON(), authenticated, myArticles.Preview)
	articles.GET("/:article_id", authenticated, myArticles.Get)
	articles.PATCH("/:article_id", middleware.RequireJSON(), authenticated, myArticles.Update)
	articles.DELETE("/:article_id", authenticated, myArticles.Delete)
	articles.POST("/:article_id/publish", authenticated, myArticles.Publish)
	articles.POST("/:article_id/offline", authenticated, myArticles.Offline)
}

// registerAdminArticleRoutes 装配管理员可见性端点：身份校验之后还要通过当前数据库角色检查。
func registerAdminArticleRoutes(h *server.Hertz, options *AuthOptions, logger *slog.Logger) {
	adminArticles := handler.NewAdminArticle(options.AdminArticles, logger)
	articles := h.Group("/api/v1/admin/articles")
	articles.POST("/:article_id/offline", middleware.Authenticate(options.Service), middleware.RequireAdmin(), adminArticles.Offline)
	articles.POST("/:article_id/restore", middleware.Authenticate(options.Service), middleware.RequireAdmin(), adminArticles.Restore)
}
