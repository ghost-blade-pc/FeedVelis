package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
)

func AccessLog(logger *slog.Logger) app.HandlerFunc {
	return func(c context.Context, ctx *app.RequestContext) {
		startedAt := time.Now()
		ctx.Next(c)
		logger.InfoContext(c, "HTTP request",
			"request_id", RequestIDFrom(ctx),
			"method", string(ctx.Method()),
			"path", string(ctx.Path()),
			"status", ctx.Response.StatusCode(),
			"duration_ms", time.Since(startedAt).Milliseconds(),
		)
	}
}
