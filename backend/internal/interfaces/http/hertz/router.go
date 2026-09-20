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
}
