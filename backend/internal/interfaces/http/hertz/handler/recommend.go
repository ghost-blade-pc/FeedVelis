package handler

import (
	"context"
	"errors"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/dto"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/middleware"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/presenter"
)

type RecommendService interface {
	Get(context.Context, string, int, string) (recommendation.Page, error)
}

type Recommend struct{ service RecommendService }

func NewRecommend(service RecommendService) *Recommend { return &Recommend{service: service} }

func (h *Recommend) Articles(ctx context.Context, c *app.RequestContext) {
	args := c.Request.URI().QueryArgs()
	counts := map[string]int{}
	args.VisitAll(func(key, _ []byte) { counts[string(key)]++ })
	for key, count := range counts {
		if count != 1 || key != "limit" && key != "cursor" {
			presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeValidationFailed, "推荐查询参数无效", middleware.RequestIDFrom(c))
			return
		}
	}
	limit := 20
	if args.Has("limit") {
		parsed, err := strconv.Atoi(c.Query("limit"))
		if err != nil || parsed < 1 || parsed > 50 {
			presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeValidationFailed, "limit 必须介于 1 和 50", middleware.RequestIDFrom(c))
			return
		}
		limit = parsed
	}
	if args.Has("cursor") && c.Query("cursor") == "" {
		presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeInvalidCursor, "推荐游标无效", middleware.RequestIDFrom(c))
		return
	}
	userID := ""
	if identity, ok := middleware.IdentityFrom(c); ok {
		userID = identity.User.ID.String()
	}
	page, err := h.service.Get(searchApp.WithRequestID(ctx, middleware.RequestIDFrom(c)), userID, limit, c.Query("cursor"))
	if err != nil {
		switch {
		case errors.Is(err, recommendation.ErrInvalidCursor):
			presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeInvalidCursor, "推荐游标无效", middleware.RequestIDFrom(c))
		case errors.Is(err, recommendation.ErrInvalidRequest):
			presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeValidationFailed, "推荐查询参数无效", middleware.RequestIDFrom(c))
		case errors.Is(err, recommendation.ErrDependencyUnavailable):
			presenter.WriteError(c, consts.StatusServiceUnavailable, presenter.CodeDependencyUnavailable, "推荐依赖暂不可用", middleware.RequestIDFrom(c))
		default:
			presenter.WriteError(c, consts.StatusInternalServerError, presenter.CodeInternalError, "推荐暂不可用", middleware.RequestIDFrom(c))
		}
		return
	}
	items := make([]dto.ArticleRecommendItem, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, dto.ArticleRecommendItem{ArticleItem: toArticleItem(item.Article), RecommendationReason: item.Reason})
	}
	c.JSON(consts.StatusOK, dto.ArticleRecommendPage{Items: items, NextCursor: page.NextCursor, HasMore: page.HasMore, Mode: page.Mode, Degraded: page.Degraded})
}
