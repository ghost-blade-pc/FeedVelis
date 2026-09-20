package middleware

import (
	"context"
	"log/slog"
	"runtime/debug"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/presenter"
)

func Recovery(logger *slog.Logger) app.HandlerFunc {
	return func(c context.Context, ctx *app.RequestContext) {
		defer func() {
			if recovered := recover(); recovered != nil {
				requestID := RequestIDFrom(ctx)
				logger.ErrorContext(c, "HTTP handler panic",
					"request_id", requestID,
					"panic", recovered,
					"stack", string(debug.Stack()),
				)
				ctx.Response.Reset()
				ctx.Abort()
				presenter.WriteError(ctx, consts.StatusInternalServerError, presenter.CodeInternalError, "服务器内部错误", requestID)
			}
		}()
		ctx.Next(c)
	}
}
