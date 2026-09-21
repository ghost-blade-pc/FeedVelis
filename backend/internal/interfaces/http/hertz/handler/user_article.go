package handler

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/dto"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/middleware"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/presenter"
)

// maxArticleBodyBytes 覆盖 256 KiB Markdown 加 JSON 转义与字段开销后的上限。
const maxArticleBodyBytes = 320 << 10

// UserArticleService 是本人文章用例在本层的消费方接口。
type UserArticleService interface {
	Create(context.Context, articleApp.CreateUserArticleCommand) (articleApp.UserArticleResult, bool, error)
	Get(context.Context, string, int64) (articleApp.UserArticleDetail, error)
	List(context.Context, string, string, int) (articleApp.UserArticlePage, error)
	Update(context.Context, articleApp.UpdateUserArticleCommand) (articleApp.UserArticleResult, bool, error)
	Publish(context.Context, articleApp.UserArticleStateCommand) (articleApp.UserArticleResult, bool, error)
	Offline(context.Context, articleApp.UserArticleStateCommand) (articleApp.UserArticleResult, bool, error)
	Delete(context.Context, articleApp.UserArticleStateCommand) (articleApp.UserArticleResult, bool, error)
	Preview(context.Context, articleApp.PreviewArticleCommand) (articleApp.ArticlePreview, error)
}

// UserArticle 处理 /api/v1/me/articles：全部端点都要求已认证身份。
// 幂等键与 If-Match 在此统一解析为显式值，应用层不接触 Hertz 请求对象。
type UserArticle struct {
	service UserArticleService
	logger  *slog.Logger
}

func NewUserArticle(service UserArticleService, logger *slog.Logger) *UserArticle {
	return &UserArticle{service: service, logger: logger}
}

func (h *UserArticle) Create(ctx context.Context, c *app.RequestContext) {
	authorID, ok := h.authorID(c)
	if !ok {
		return
	}
	key, ok := h.idempotencyKey(ctx, c)
	if !ok {
		return
	}
	var request dto.CreateArticleRequest
	if !decodeContentBody(c, maxArticleBodyBytes, &request) {
		return
	}
	status, ok := initialStatus(c, request.InitialStatus)
	if !ok {
		return
	}
	result, _, err := h.service.Create(ctx, articleApp.CreateUserArticleCommand{
		AuthorUserID: authorID, IdempotencyKey: key,
		Title: request.Title, Markdown: request.Markdown, InitialStatus: status,
	})
	if err != nil {
		h.reject(ctx, c, err)
		return
	}
	h.writeResult(c, consts.StatusCreated, result)
}

func (h *UserArticle) List(ctx context.Context, c *app.RequestContext) {
	authorID, ok := h.authorID(c)
	if !ok {
		return
	}
	limit, ok := contentLimit(c)
	if !ok {
		return
	}
	page, err := h.service.List(ctx, authorID, c.Query("cursor"), limit)
	if err != nil {
		h.reject(ctx, c, err)
		return
	}
	items := make([]dto.MyArticleSummary, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, toMyArticleSummary(item))
	}
	c.JSON(consts.StatusOK, dto.MyArticlePage{Items: items, NextCursor: page.NextCursor, HasMore: page.HasMore})
}

func (h *UserArticle) Get(ctx context.Context, c *app.RequestContext) {
	authorID, ok := h.authorID(c)
	if !ok {
		return
	}
	articleID, ok := articleIDParam(c)
	if !ok {
		return
	}
	detail, err := h.service.Get(ctx, authorID, articleID)
	if err != nil {
		h.reject(ctx, c, err)
		return
	}
	h.writeDetail(c, consts.StatusOK, detail)
}

func (h *UserArticle) Update(ctx context.Context, c *app.RequestContext) {
	authorID, ok := h.authorID(c)
	if !ok {
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
	var request dto.UpdateArticleRequest
	if !decodeContentBody(c, maxArticleBodyBytes, &request) {
		return
	}
	result, _, err := h.service.Update(ctx, articleApp.UpdateUserArticleCommand{
		AuthorUserID: authorID, ArticleID: articleID, ExpectedVersion: expected, IdempotencyKey: key,
		Title: request.Title, Markdown: request.Markdown,
	})
	if err != nil {
		h.reject(ctx, c, err)
		return
	}
	h.writeResult(c, consts.StatusOK, result)
}

func (h *UserArticle) Publish(ctx context.Context, c *app.RequestContext) {
	h.changeState(ctx, c, h.service.Publish)
}

func (h *UserArticle) Offline(ctx context.Context, c *app.RequestContext) {
	h.changeState(ctx, c, h.service.Offline)
}

// Delete 成功时返回 204：删除是终态，调用者不需要再读取聚合内容。
func (h *UserArticle) Delete(ctx context.Context, c *app.RequestContext) {
	authorID, ok := h.authorID(c)
	if !ok {
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
	if _, _, err := h.service.Delete(ctx, articleApp.UserArticleStateCommand{
		AuthorUserID: authorID, ArticleID: articleID, ExpectedVersion: expected, IdempotencyKey: key,
	}); err != nil {
		h.reject(ctx, c, err)
		return
	}
	c.Status(consts.StatusNoContent)
}

// Preview 无状态渲染：不需要幂等键或 If-Match，也不写入任何状态。
func (h *UserArticle) Preview(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.authorID(c); !ok {
		return
	}
	var request dto.PreviewArticleRequest
	if !decodeContentBody(c, maxArticleBodyBytes, &request) {
		return
	}
	preview, err := h.service.Preview(ctx, articleApp.PreviewArticleCommand{Title: request.Title, Markdown: request.Markdown})
	if err != nil {
		h.reject(ctx, c, err)
		return
	}
	assetIDs := preview.AssetIDs
	if assetIDs == nil {
		assetIDs = []string{}
	}
	c.JSON(consts.StatusOK, dto.ArticlePreview{
		ContentHTML: preview.ContentHTML, PlainText: preview.PlainText,
		Excerpt: preview.Excerpt, AssetIDs: assetIDs,
	})
}

type stateChange func(context.Context, articleApp.UserArticleStateCommand) (articleApp.UserArticleResult, bool, error)

func (h *UserArticle) changeState(ctx context.Context, c *app.RequestContext, change stateChange) {
	authorID, ok := h.authorID(c)
	if !ok {
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
	result, _, err := change(ctx, articleApp.UserArticleStateCommand{
		AuthorUserID: authorID, ArticleID: articleID, ExpectedVersion: expected, IdempotencyKey: key,
	})
	if err != nil {
		h.reject(ctx, c, err)
		return
	}
	h.writeResult(c, consts.StatusOK, result)
}

func (h *UserArticle) authorID(c *app.RequestContext) (string, bool) {
	identity, ok := middleware.IdentityFrom(c)
	if !ok || identity.User.ID.IsZero() {
		presenter.WriteError(c, consts.StatusUnauthorized, presenter.CodeSessionInvalid, "登录状态无效，请重新登录", middleware.RequestIDFrom(c))
		return "", false
	}
	return identity.User.ID.String(), true
}

func (h *UserArticle) idempotencyKey(ctx context.Context, c *app.RequestContext) (string, bool) {
	key, err := presenter.ParseIdempotencyKey(string(c.Request.Header.Peek(presenter.HeaderIdempotencyKey)))
	if err != nil {
		h.reject(ctx, c, err)
		return "", false
	}
	return key, true
}

func (h *UserArticle) ifMatch(ctx context.Context, c *app.RequestContext) (int64, bool) {
	version, err := presenter.ParseIfMatch(string(c.Request.Header.Peek(presenter.HeaderIfMatch)))
	if err != nil {
		h.reject(ctx, c, err)
		return 0, false
	}
	return version, true
}

func (h *UserArticle) reject(ctx context.Context, c *app.RequestContext, err error) {
	mapping := presenter.MapContentError(err)
	if h.logger != nil {
		h.logger.InfoContext(ctx, "内容写入失败",
			"code", mapping.Code, "request_id", middleware.RequestIDFrom(c))
	}
	presenter.WriteMapping(c, mapping, middleware.RequestIDFrom(c))
}

// writeResult 写出新建或状态变更结果，并把当前 lock_version 作为强 ETag 返回。
func (h *UserArticle) writeResult(c *app.RequestContext, status int, result articleApp.UserArticleResult) {
	c.Header(presenter.HeaderETag, presenter.ETag(result.Article.LockVersion))
	c.JSON(status, toMyArticleDetail(result, result.AssetIDs))
}

func (h *UserArticle) writeDetail(c *app.RequestContext, status int, detail articleApp.UserArticleDetail) {
	c.Header(presenter.HeaderETag, presenter.ETag(detail.Article.LockVersion))
	c.JSON(status, toMyArticleDetail(articleApp.UserArticleResult{Article: detail.Article}, detail.AssetIDs))
}

func toMyArticleSummary(stored articleDomain.StoredArticle) dto.MyArticleSummary {
	var reason *string
	if stored.OfflineReason != nil {
		value := string(*stored.OfflineReason)
		reason = &value
	}
	return dto.MyArticleSummary{
		ID: stored.ID, Title: stored.Revision.Title, Status: string(stored.Status), OfflineReason: reason,
		RevisionNo: stored.RevisionNumber, LockVersion: stored.LockVersion,
		PublishedAt: utcTime(stored.PublishedAt), UpdatedAt: stored.UpdatedAt.UTC(),
	}
}

func toMyArticleDetail(result articleApp.UserArticleResult, assetIDs []string) dto.MyArticleDetail {
	stored := result.Article
	markdown := ""
	if stored.Revision.Markdown != nil {
		markdown = *stored.Revision.Markdown
	}
	contentHTML := ""
	if stored.Revision.SanitizedHTML != nil {
		contentHTML = *stored.Revision.SanitizedHTML
	}
	// 空数组也要出现在响应里：契约要求 asset_ids 始终是数组。
	if assetIDs == nil {
		assetIDs = []string{}
	}
	return dto.MyArticleDetail{
		MyArticleSummary: toMyArticleSummary(stored),
		Markdown:         markdown, ContentHTML: contentHTML,
		Excerpt: stored.Revision.Excerpt, AssetIDs: assetIDs,
	}
}

func decodeContentBody(c *app.RequestContext, maxBytes int, target any) bool {
	if err := presenter.DecodeStrictJSON(c.Request.Body(), maxBytes, target); err != nil {
		presenter.WriteMapping(c, presenter.MapContentError(err), middleware.RequestIDFrom(c))
		return false
	}
	return true
}

func articleIDParam(c *app.RequestContext) (int64, bool) {
	articleID, err := strconv.ParseInt(c.Param("article_id"), 10, 64)
	if err != nil || articleID <= 0 {
		presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeValidationFailed, "文章 ID 无效", middleware.RequestIDFrom(c))
		return 0, false
	}
	return articleID, true
}

func contentLimit(c *app.RequestContext) (int, bool) {
	raw := c.Query("limit")
	if raw == "" {
		return 20, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil {
		presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeValidationFailed, "limit 必须是整数", middleware.RequestIDFrom(c))
		return 0, false
	}
	return limit, true
}

func initialStatus(c *app.RequestContext, raw string) (articleDomain.Status, bool) {
	status := articleDomain.Status(raw)
	if status != articleDomain.StatusDraft && status != articleDomain.StatusPublished {
		presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeValidationFailed,
			"initial_status 必须是 draft 或 published", middleware.RequestIDFrom(c))
		return "", false
	}
	return status, true
}
