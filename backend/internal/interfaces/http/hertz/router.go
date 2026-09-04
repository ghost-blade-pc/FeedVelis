// Package hertz 负责装配 HTTP 路由和协议级中间件。
package hertz

import (
	"context"
	"log/slog"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/handler"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/middleware"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/presenter"
)

func NewServer(address string, shutdownTimeout time.Duration, logger *slog.Logger, healthService *health.Service) *server.Hertz {
	h := server.New(
		server.WithHostPorts(address),
		server.WithExitWaitTime(shutdownTimeout),
		server.WithDisablePrintRoute(true),
	)

	// 固定顺序：Request ID → Recovery → Access Log。CORS、Auth 与限流在相应用例落地时接入。
	h.Use(middleware.RequestID, middleware.Recovery(logger), middleware.AccessLog(logger))

	healthHandler := handler.NewHealth(healthService)
	h.GET("/livez", healthHandler.Live)
	h.GET("/readyz", healthHandler.Ready)
	h.GET("/api/v1/ping", func(_ context.Context, c *app.RequestContext) {
		c.JSON(consts.StatusOK, map[string]string{"message": "pong"})
	})
	h.NoRoute(func(_ context.Context, c *app.RequestContext) {
		presenter.WriteNotFound(c, middleware.RequestIDFrom(c))
	})
	return h
}
