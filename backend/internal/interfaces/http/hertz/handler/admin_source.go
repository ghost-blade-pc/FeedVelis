package handler

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	sourceApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/source"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/dto"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/middleware"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/presenter"
)

// maxSourceBodyBytes 覆盖 URL 与周期两个字段加 JSON 开销。
const maxSourceBodyBytes = 16 << 10

// AdminSourceService 是管理员 Source 用例在本层的消费方接口。
type AdminSourceService interface {
	Create(context.Context, sourceApp.CreateCommand) (sourceApp.CreateResult, bool, error)
	List(context.Context) ([]sourceDomain.Source, error)
	Get(context.Context, int64) (sourceDomain.Source, error)
	Pause(context.Context, sourceApp.SourceCommand) (sourceDomain.Source, bool, error)
	Resume(context.Context, sourceApp.SourceCommand) (sourceDomain.Source, bool, error)
	SetInterval(context.Context, sourceApp.SourceCommand) (sourceDomain.Source, bool, error)
	FetchNow(context.Context, sourceApp.FetchCommand) (sourceApp.FetchRunResult, error)
	History(context.Context, sourceApp.HistoryCommand) (sourceApp.HistoryPage, error)
}

// AdminSource 处理 /api/v1/admin/sources：角色检查由中间件完成，用例如需再次确认身份。
type AdminSource struct {
	service AdminSourceService
	logger  *slog.Logger
}

func NewAdminSource(service AdminSourceService, logger *slog.Logger) *AdminSource {
	return &AdminSource{service: service, logger: logger}
}

func (h *AdminSource) List(ctx context.Context, c *app.RequestContext) {
	sources, err := h.service.List(ctx)
	if err != nil {
		h.reject(ctx, c, err)
		return
	}
	items := make([]dto.AdminSource, 0, len(sources))
	for _, source := range sources {
		items = append(items, toAdminSource(source))
	}
	c.JSON(consts.StatusOK, dto.SourcePage{Items: items, NextCursor: nil, HasMore: false})
}

func (h *AdminSource) Create(ctx context.Context, c *app.RequestContext) {
	actorID, ok := h.actor(c)
	if !ok {
		return
	}
	key, ok := h.idempotencyKey(ctx, c)
	if !ok {
		return
	}
	var request dto.CreateSourceRequest
	if err := presenter.DecodeStrictJSON(c.Request.Body(), maxSourceBodyBytes, &request); err != nil {
		h.reject(ctx, c, err)
		return
	}
	command := sourceApp.CreateCommand{ActorUserID: actorID, IdempotencyKey: key, FeedURL: request.FeedURL}
	if request.FetchIntervalSeconds != nil {
		command.FetchInterval = time.Duration(*request.FetchIntervalSeconds) * time.Second
	}
	result, _, err := h.service.Create(ctx, command)
	if err != nil {
		h.reject(ctx, c, err)
		return
	}
	// 规范化 URL 已存在时返回明确冲突，避免客户端以为创建了第二个调度身份。
	if !result.Created {
		h.reject(ctx, c, sourceApp.ErrSourceExists)
		return
	}
	h.writeSource(c, consts.StatusCreated, result.Source)
}

func (h *AdminSource) Get(ctx context.Context, c *app.RequestContext) {
	sourceID, ok := sourceIDParam(c)
	if !ok {
		return
	}
	source, err := h.service.Get(ctx, sourceID)
	if err != nil {
		h.reject(ctx, c, err)
		return
	}
	h.writeSource(c, consts.StatusOK, source)
}

func (h *AdminSource) Update(ctx context.Context, c *app.RequestContext) {
	actorID, ok := h.actor(c)
	if !ok {
		return
	}
	key, ok := h.idempotencyKey(ctx, c)
	if !ok {
		return
	}
	sourceID, ok := sourceIDParam(c)
	if !ok {
		return
	}
	expected, ok := h.ifMatch(ctx, c)
	if !ok {
		return
	}
	var request dto.UpdateSourceRequest
	if err := presenter.DecodeStrictJSON(c.Request.Body(), maxSourceBodyBytes, &request); err != nil {
		h.reject(ctx, c, err)
		return
	}
	updated, _, err := h.service.SetInterval(ctx, sourceApp.SourceCommand{
		ActorUserID: actorID, IdempotencyKey: key, SourceID: sourceID, ExpectedVersion: expected,
		Interval: time.Duration(request.FetchIntervalSeconds) * time.Second,
	})
	if err != nil {
		h.reject(ctx, c, err)
		return
	}
	h.writeSource(c, consts.StatusOK, updated)
}

func (h *AdminSource) Pause(ctx context.Context, c *app.RequestContext) {
	h.changeState(ctx, c, h.service.Pause)
}

func (h *AdminSource) Resume(ctx context.Context, c *app.RequestContext) {
	h.changeState(ctx, c, h.service.Resume)
}

// Fetch 同步触发一次抓取；运行中的同键重试返回同一运行 ID 而不是再次访问上游。
func (h *AdminSource) Fetch(ctx context.Context, c *app.RequestContext) {
	actorID, ok := h.actor(c)
	if !ok {
		return
	}
	key, ok := h.idempotencyKey(ctx, c)
	if !ok {
		return
	}
	sourceID, ok := sourceIDParam(c)
	if !ok {
		return
	}
	result, err := h.service.FetchNow(ctx, sourceApp.FetchCommand{
		ActorUserID: actorID, IdempotencyKey: key, SourceID: sourceID})
	if err != nil {
		h.reject(ctx, c, err)
		return
	}
	c.JSON(consts.StatusOK, toSourceFetchRun(result.Run))
}

func (h *AdminSource) History(ctx context.Context, c *app.RequestContext) {
	sourceID, ok := sourceIDParam(c)
	if !ok {
		return
	}
	limit, ok := historyLimit(c)
	if !ok {
		return
	}
	page, err := h.service.History(ctx, sourceApp.HistoryCommand{
		SourceID: sourceID, Cursor: c.Query("cursor"), Limit: limit})
	if err != nil {
		h.reject(ctx, c, err)
		return
	}
	items := make([]dto.SourceFetchRun, 0, len(page.Items))
	for _, run := range page.Items {
		items = append(items, toSourceFetchRun(run))
	}
	c.JSON(consts.StatusOK, dto.SourceFetchRunPage{Items: items, NextCursor: page.NextCursor, HasMore: page.HasMore})
}

type sourceStateChange func(context.Context, sourceApp.SourceCommand) (sourceDomain.Source, bool, error)

func (h *AdminSource) changeState(ctx context.Context, c *app.RequestContext, change sourceStateChange) {
	actorID, ok := h.actor(c)
	if !ok {
		return
	}
	key, ok := h.idempotencyKey(ctx, c)
	if !ok {
		return
	}
	sourceID, ok := sourceIDParam(c)
	if !ok {
		return
	}
	expected, ok := h.ifMatch(ctx, c)
	if !ok {
		return
	}
	updated, _, err := change(ctx, sourceApp.SourceCommand{
		ActorUserID: actorID, IdempotencyKey: key, SourceID: sourceID, ExpectedVersion: expected})
	if err != nil {
		h.reject(ctx, c, err)
		return
	}
	h.writeSource(c, consts.StatusOK, updated)
}

func (h *AdminSource) actor(c *app.RequestContext) (string, bool) {
	identity, ok := middleware.IdentityFrom(c)
	if !ok || identity.User.ID.IsZero() {
		presenter.WriteError(c, consts.StatusUnauthorized, presenter.CodeSessionInvalid, "登录状态无效，请重新登录", middleware.RequestIDFrom(c))
		return "", false
	}
	return identity.User.ID.String(), true
}

func (h *AdminSource) idempotencyKey(ctx context.Context, c *app.RequestContext) (string, bool) {
	key, err := presenter.ParseIdempotencyKey(string(c.Request.Header.Peek(presenter.HeaderIdempotencyKey)))
	if err != nil {
		h.reject(ctx, c, err)
		return "", false
	}
	return key, true
}

func (h *AdminSource) ifMatch(ctx context.Context, c *app.RequestContext) (int64, bool) {
	version, err := presenter.ParseIfMatch(string(c.Request.Header.Peek(presenter.HeaderIfMatch)))
	if err != nil {
		h.reject(ctx, c, err)
		return 0, false
	}
	return version, true
}

func (h *AdminSource) reject(ctx context.Context, c *app.RequestContext, err error) {
	mapping := presenter.MapSourceError(err)
	if h.logger != nil {
		h.logger.InfoContext(ctx, "来源管理请求失败", "code", mapping.Code, "request_id", middleware.RequestIDFrom(c))
	}
	presenter.WriteMapping(c, mapping, middleware.RequestIDFrom(c))
}

func (h *AdminSource) writeSource(c *app.RequestContext, status int, source sourceDomain.Source) {
	c.Header(presenter.HeaderETag, presenter.ETag(source.LockVersion))
	c.JSON(status, toAdminSource(source))
}

func toAdminSource(source sourceDomain.Source) dto.AdminSource {
	nextFetchAt := source.NextFetchAt.UTC()
	return dto.AdminSource{
		ID: source.ID, FeedURL: source.FeedURL, Title: source.Title, Status: string(source.Status),
		FetchIntervalSeconds: int64(source.FetchIntervalOr() / time.Second),
		LockVersion:          source.LockVersion, NextFetchAt: &nextFetchAt,
		LastSuccessAt: utcTime(source.LastSuccessAt), ConsecutiveFailures: source.ConsecutiveFailures,
		LastErrorCode: source.LastErrorCode, CreatedAt: source.CreatedAt.UTC(), UpdatedAt: source.UpdatedAt.UTC(),
	}
}

func toSourceFetchRun(run sourceDomain.FetchRun) dto.SourceFetchRun {
	return dto.SourceFetchRun{
		ID: run.ID, SourceID: run.SourceID, Trigger: string(run.Trigger), Status: string(run.Status),
		NotModified: run.NotModified, InsertedCount: run.Inserted, UpdatedCount: run.Updated,
		UnchangedCount: run.Unchanged, SkippedCount: run.Skipped, ErrorCode: run.ErrorCode,
		StartedAt: run.StartedAt.UTC(), CompletedAt: utcTime(run.CompletedAt),
	}
}

func sourceIDParam(c *app.RequestContext) (int64, bool) {
	sourceID, err := strconv.ParseInt(c.Param("source_id"), 10, 64)
	if err != nil || sourceID <= 0 {
		presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeValidationFailed, "来源 ID 无效", middleware.RequestIDFrom(c))
		return 0, false
	}
	return sourceID, true
}

func historyLimit(c *app.RequestContext) (int, bool) {
	raw := c.Query("limit")
	if raw == "" {
		return 0, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil {
		presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeValidationFailed, "limit 必须是整数", middleware.RequestIDFrom(c))
		return 0, false
	}
	return limit, true
}
