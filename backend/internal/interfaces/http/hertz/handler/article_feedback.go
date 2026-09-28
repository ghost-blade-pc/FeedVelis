package handler

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	feedback "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/articlefeedback"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/middleware"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/presenter"
)

type ArticleFeedbackService interface {
	RecordRead(context.Context, string, int64) error
	SetFavorite(context.Context, string, int64, bool) error
	SetNotInterested(context.Context, string, int64, bool) error
	States(context.Context, string, []int64) ([]feedback.State, error)
}

type ArticleFeedback struct{ service ArticleFeedbackService }

func NewArticleFeedback(service ArticleFeedbackService) *ArticleFeedback {
	return &ArticleFeedback{service: service}
}

func (h *ArticleFeedback) Read(ctx context.Context, c *app.RequestContext) {
	h.write(ctx, c, h.service.RecordRead)
}
func (h *ArticleFeedback) FavoritePut(ctx context.Context, c *app.RequestContext) {
	h.write(ctx, c, func(ctx context.Context, user string, id int64) error {
		return h.service.SetFavorite(ctx, user, id, true)
	})
}
func (h *ArticleFeedback) FavoriteDelete(ctx context.Context, c *app.RequestContext) {
	h.write(ctx, c, func(ctx context.Context, user string, id int64) error {
		return h.service.SetFavorite(ctx, user, id, false)
	})
}
func (h *ArticleFeedback) NotInterestedPut(ctx context.Context, c *app.RequestContext) {
	h.write(ctx, c, func(ctx context.Context, user string, id int64) error {
		return h.service.SetNotInterested(ctx, user, id, true)
	})
}
func (h *ArticleFeedback) NotInterestedDelete(ctx context.Context, c *app.RequestContext) {
	h.write(ctx, c, func(ctx context.Context, user string, id int64) error {
		return h.service.SetNotInterested(ctx, user, id, false)
	})
}

func (h *ArticleFeedback) write(ctx context.Context, c *app.RequestContext, action func(context.Context, string, int64) error) {
	if len(c.Request.Body()) != 0 {
		h.validation(c)
		return
	}
	id, err := strconv.ParseInt(c.Param("article_id"), 10, 64)
	if err != nil || id <= 0 || !validFeedbackID(c.Param("article_id")) {
		h.validation(c)
		return
	}
	user, ok := feedbackUser(c)
	if !ok {
		return
	}
	if err := action(ctx, user, id); err != nil {
		h.reject(c, err)
		return
	}
	c.SetStatusCode(consts.StatusNoContent)
}

func (h *ArticleFeedback) States(ctx context.Context, c *app.RequestContext) {
	if len(c.Request.Body()) != 0 {
		h.validation(c)
		return
	}
	raw := c.Query("article_ids")
	parts := strings.Split(raw, ",")
	if len(parts) < 1 || len(parts) > 50 {
		h.validation(c)
		return
	}
	ids := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 || !validFeedbackID(part) {
			h.validation(c)
			return
		}
		if _, ok := seen[id]; ok {
			h.validation(c)
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	user, ok := feedbackUser(c)
	if !ok {
		return
	}
	items, err := h.service.States(ctx, user, ids)
	if err != nil {
		h.reject(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"items": items})
}

func validFeedbackID(raw string) bool {
	if len(raw) == 0 || raw[0] < '1' || raw[0] > '9' {
		return false
	}
	for i := 1; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return false
		}
	}
	return true
}

func feedbackUser(c *app.RequestContext) (string, bool) {
	identity, ok := middleware.IdentityFrom(c)
	if !ok || identity.User.ID.IsZero() {
		presenter.WriteError(c, consts.StatusUnauthorized, presenter.CodeSessionInvalid, "登录状态无效，请重新登录", middleware.RequestIDFrom(c))
		return "", false
	}
	return identity.User.ID.String(), true
}

func (h *ArticleFeedback) validation(c *app.RequestContext) {
	presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeValidationFailed, "反馈参数无效", middleware.RequestIDFrom(c))
}
func (h *ArticleFeedback) reject(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, feedback.ErrInvalidInput):
		h.validation(c)
	case errors.Is(err, feedback.ErrArticleNotFound):
		presenter.WriteError(c, consts.StatusNotFound, presenter.CodeArticleNotFound, "文章不存在或不可见", middleware.RequestIDFrom(c))
	default:
		presenter.WriteError(c, consts.StatusInternalServerError, presenter.CodeInternalError, "反馈服务暂不可用", middleware.RequestIDFrom(c))
	}
}
