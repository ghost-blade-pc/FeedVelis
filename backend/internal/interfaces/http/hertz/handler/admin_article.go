package handler

import (
	"context"
	"log/slog"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/dto"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/middleware"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/presenter"
)

// AdminArticleService 是管理员文章用例在本层的消费方接口。
// 管理员只能下架与恢复，没有草稿读取、投稿编辑或删除入口。
type AdminArticleService interface {
	Offline(context.Context, articleApp.AdminArticleCommand) (articleApp.UserArticleResult, bool, error)
	Restore(context.Context, articleApp.AdminArticleCommand) (articleApp.UserArticleResult, bool, error)
}

type AdminArticle struct {
	service AdminArticleService
	logger  *slog.Logger
}

func NewAdminArticle(service AdminArticleService, logger *slog.Logger) *AdminArticle {
	return &AdminArticle{service: service, logger: logger}
}

func (h *AdminArticle) Offline(ctx context.Context, c *app.RequestContext) {
	h.change(ctx, c, h.service.Offline)
}

func (h *AdminArticle) Restore(ctx context.Context, c *app.RequestContext) {
	h.change(ctx, c, h.service.Restore)
}

type adminChange func(context.Context, articleApp.AdminArticleCommand) (articleApp.UserArticleResult, bool, error)

func (h *AdminArticle) change(ctx context.Context, c *app.RequestContext, change adminChange) {
	identity, ok := middleware.IdentityFrom(c)
	if !ok {
		presenter.WriteError(c, consts.StatusUnauthorized, presenter.CodeSessionInvalid, "登录状态无效，请重新登录", middleware.RequestIDFrom(c))
		return
	}
	key, ok := h.idempotencyKey(ctx, c)
	if !ok {
		return
	}
	articleID, ok := articleIDParam(c)
	if !ok {
		return
	}
	expected, ok := h.ifMatch(ctx, c)
	if !ok {
		return
	}
	// 角色在用例内以数据库当前值为准再次校验，中间件只做入口收窄。
	result, _, err := change(ctx, articleApp.AdminArticleCommand{
		Actor:     articleApp.AdminActor{UserID: identity.User.ID.String(), Role: identity.User.Role},
		ArticleID: articleID, ExpectedVersion: expected, IdempotencyKey: key,
	})
	if err != nil {
		h.reject(ctx, c, err)
		return
	}
	c.Header(presenter.HeaderETag, presenter.ETag(result.Article.LockVersion))
	c.JSON(consts.StatusOK, toAdminArticleResult(result.Article))
}

func (h *AdminArticle) idempotencyKey(ctx context.Context, c *app.RequestContext) (string, bool) {
	key, err := presenter.ParseIdempotencyKey(string(c.Request.Header.Peek(presenter.HeaderIdempotencyKey)))
	if err != nil {
		h.reject(ctx, c, err)
		return "", false
	}
	return key, true
}

func (h *AdminArticle) ifMatch(ctx context.Context, c *app.RequestContext) (int64, bool) {
	version, err := presenter.ParseIfMatch(string(c.Request.Header.Peek(presenter.HeaderIfMatch)))
	if err != nil {
		h.reject(ctx, c, err)
		return 0, false
	}
	return version, true
}

func (h *AdminArticle) reject(ctx context.Context, c *app.RequestContext, err error) {
	mapping := presenter.MapContentError(err)
	if h.logger != nil {
		h.logger.InfoContext(ctx, "管理员文章操作失败", "code", mapping.Code, "request_id", middleware.RequestIDFrom(c))
	}
	presenter.WriteMapping(c, mapping, middleware.RequestIDFrom(c))
}

func toAdminArticleResult(stored articleDomain.StoredArticle) dto.AdminArticleResult {
	var reason *string
	if stored.OfflineReason != nil {
		value := string(*stored.OfflineReason)
		reason = &value
	}
	// 管理员只能看到 published 与 offline(admin)，数据库 CHECK 保证其 published_at 非空。
	var publishedAt time.Time
	if stored.PublishedAt != nil {
		publishedAt = stored.PublishedAt.UTC()
	}
	return dto.AdminArticleResult{
		ID: stored.ID, Status: string(stored.Status), OfflineReason: reason,
		LockVersion: stored.LockVersion, PublishedAt: publishedAt,
	}
}
