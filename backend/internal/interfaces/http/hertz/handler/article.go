package handler

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/dto"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/middleware"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/presenter"
)

type Article struct{ service *articleApp.Service }

func NewArticle(service *articleApp.Service) *Article { return &Article{service: service} }

func (h *Article) List(ctx context.Context, c *app.RequestContext) {
	limit := 20
	if raw := c.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeValidationFailed, "limit 必须是整数", middleware.RequestIDFrom(c))
			return
		}
		limit = parsed
	}
	page, err := h.service.List(ctx, c.Query("cursor"), limit)
	if err != nil {
		switch {
		case errors.Is(err, articleDomain.ErrInvalidCursor):
			presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeInvalidCursor, "游标无效或版本不受支持", middleware.RequestIDFrom(c))
		case errors.Is(err, articleDomain.ErrInvalidArgument):
			presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeValidationFailed, "limit 必须介于 1 和 50", middleware.RequestIDFrom(c))
		default:
			presenter.WriteError(c, consts.StatusInternalServerError, presenter.CodeInternalError, "文章列表暂不可用", middleware.RequestIDFrom(c))
		}
		return
	}
	items := make([]dto.ArticleItem, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, toArticleItem(item))
	}
	c.JSON(consts.StatusOK, dto.ArticleListResponse{Items: items, NextCursor: page.NextCursor, HasMore: page.HasMore})
}

func (h *Article) Get(ctx context.Context, c *app.RequestContext) {
	articleID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || articleID <= 0 {
		presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeValidationFailed, "文章 ID 无效", middleware.RequestIDFrom(c))
		return
	}
	detail, err := h.service.Get(ctx, articleID)
	if err != nil {
		switch {
		case errors.Is(err, articleDomain.ErrNotFound):
			presenter.WriteError(c, consts.StatusNotFound, presenter.CodeArticleNotFound, "文章不存在或不可见", middleware.RequestIDFrom(c))
		case errors.Is(err, articleDomain.ErrInvalidArgument):
			presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeValidationFailed, "文章 ID 无效", middleware.RequestIDFrom(c))
		default:
			presenter.WriteError(c, consts.StatusInternalServerError, presenter.CodeInternalError, "文章详情暂不可用", middleware.RequestIDFrom(c))
		}
		return
	}
	c.JSON(consts.StatusOK, dto.ArticleDetailResponse{ArticleItem: toArticleItem(detail.Item), ContentHTML: detail.SanitizedHTML})
}

func toArticleItem(item articleDomain.ListItem) dto.ArticleItem {
	return dto.ArticleItem{
		ID: item.ID, Title: item.Title, CanonicalURL: item.CanonicalURL,
		Source:     dto.ArticleSource{ID: item.Source.ID, Title: item.Source.Title, SiteURL: item.Source.SiteURL},
		AuthorName: item.AuthorName, Excerpt: item.Excerpt,
		SourcePublishedAt: utcTime(item.SourcePublishedAt), DiscoveredAt: item.DiscoveredAt.UTC(),
	}
}

func utcTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}
