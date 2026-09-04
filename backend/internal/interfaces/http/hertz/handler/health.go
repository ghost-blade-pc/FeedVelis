// Package handler 包含 HTTP 协议适配，不承载业务规则。
package handler

import (
	"context"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/middleware"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/presenter"
)

type Health struct {
	service *health.Service
}

func NewHealth(service *health.Service) *Health {
	return &Health{service: service}
}

func (h *Health) Live(_ context.Context, c *app.RequestContext) {
	c.JSON(consts.StatusOK, map[string]string{"status": "ok"})
}

func (h *Health) Ready(ctx context.Context, c *app.RequestContext) {
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := h.service.Ready(checkCtx); err != nil {
		presenter.WriteError(c, consts.StatusServiceUnavailable, "NOT_READY", "服务尚未就绪", middleware.RequestIDFrom(c))
		return
	}
	c.JSON(consts.StatusOK, map[string]string{"status": "ok"})
}
