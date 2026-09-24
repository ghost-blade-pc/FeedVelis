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
	// 契约要求 content_html 始终是字符串：无正文的 RSS 条目返回空串而不是 null。
	contentHTML := ""
	if detail.SanitizedHTML != nil {
		contentHTML = *detail.SanitizedHTML
	}
	c.JSON(consts.StatusOK, dto.ArticleDetailResponse{ArticleItem: toArticleItem(detail.Item), ContentHTML: contentHTML})
}

func toArticleItem(item articleDomain.ListItem) dto.ArticleItem {
	return dto.ArticleItem{
		ID: item.ID, Title: item.Title, Excerpt: item.Excerpt,
		PublishedAt: item.SortAt.UTC(), Origin: toArticleOrigin(item), Enhancement: toArticleEnhancement(item.Enhancement),
	}
}

func toArticleEnhancement(value *articleDomain.Enhancement) *dto.ArticleEnhancement {
	if value == nil {
		return nil
	}
	return &dto.ArticleEnhancement{Summary: value.Summary, Keywords: value.Keywords, Topics: value.Topics, GeneratedAt: value.GeneratedAt.UTC()}
}

// toArticleOrigin 由来源类型选择判别联合分支；用户来源不携带 Source 或原文 URL。
func toArticleOrigin(item articleDomain.ListItem) any {
	if item.Origin == articleDomain.OriginUser && item.Author != nil {
		return dto.UserArticleOrigin{
			Type:   string(articleDomain.OriginUser),
			Author: dto.ArticleAuthor{ID: item.Author.ID, Nickname: item.Author.Nickname},
		}
	}
	return dto.RSSArticleOrigin{
		Type:              string(articleDomain.OriginRSS),
		Source:            dto.ArticleSource{ID: item.Source.ID, Title: item.Source.Title, SiteURL: item.Source.SiteURL},
		CanonicalURL:      item.CanonicalURL,
		SourcePublishedAt: utcTime(item.SourcePublishedAt),
	}
}

func utcTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}
