package handler

import (
	"context"
	"log/slog"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	assetApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asset"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	assetDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/asset"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/dto"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/middleware"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/presenter"
)

// maxAssetBodyBytes 是资产声明请求的上限：只有类型与大小两个字段。
const maxAssetBodyBytes = 1 << 10

// AssetService 是资产用例在本层的消费方接口。
type AssetService interface {
	Create(context.Context, assetApp.CreateCommand) (assetApp.Upload, bool, error)
	Confirm(context.Context, assetApp.ConfirmCommand) (assetDomain.Asset, bool, error)
	Open(context.Context, assetApp.ContentCommand) (assetApp.Content, error)
	Stat(context.Context, assetApp.ContentCommand) (ports.ObjectInfo, error)
}

// Asset 处理本人资产写入与图片字节读取。
type Asset struct {
	service AssetService
	logger  *slog.Logger
}

func NewAsset(service AssetService, logger *slog.Logger) *Asset {
	return &Asset{service: service, logger: logger}
}

func (h *Asset) Create(ctx context.Context, c *app.RequestContext) {
	ownerUserID, ok := h.owner(c)
	if !ok {
		return
	}
	key, ok := h.idempotencyKey(ctx, c)
	if !ok {
		return
	}
	var request dto.CreateAssetRequest
	if err := presenter.DecodeStrictJSON(c.Request.Body(), maxAssetBodyBytes, &request); err != nil {
		h.reject(ctx, c, err)
		return
	}
	upload, _, err := h.service.Create(ctx, assetApp.CreateCommand{
		OwnerUserID: ownerUserID, IdempotencyKey: key,
		ContentType: request.ContentType, SizeBytes: request.SizeBytes,
	})
	if err != nil {
		h.reject(ctx, c, err)
		return
	}
	headers := upload.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	c.JSON(consts.StatusCreated, dto.AssetUpload{
		Asset: toArticleAsset(upload.Asset), UploadURL: upload.URL, UploadMethod: upload.Method,
		UploadHeaders: headers, ExpiresAt: upload.ExpiresAt.UTC(),
	})
}

func (h *Asset) Confirm(ctx context.Context, c *app.RequestContext) {
	ownerUserID, ok := h.owner(c)
	if !ok {
		return
	}
	key, ok := h.idempotencyKey(ctx, c)
	if !ok {
		return
	}
	assetID, ok := assetIDParam(c)
	if !ok {
		return
	}
	stored, _, err := h.service.Confirm(ctx, assetApp.ConfirmCommand{
		OwnerUserID: ownerUserID, AssetID: assetID, IdempotencyKey: key})
	if err != nil {
		h.reject(ctx, c, err)
		return
	}
	c.JSON(consts.StatusOK, toArticleAsset(stored))
}

// ContentGet 流式转发对象字节：不把完整对象读进内存。
func (h *Asset) ContentGet(ctx context.Context, c *app.RequestContext) {
	assetID, ok := assetIDParam(c)
	if !ok {
		return
	}
	content, err := h.service.Open(ctx, assetApp.ContentCommand{AssetID: assetID, ViewerUserID: h.viewerID(c)})
	if err != nil {
		h.reject(ctx, c, err)
		return
	}
	// SetBodyStream 接管 Body，并在响应发送完成后关闭。这里提前 defer Close 会让真实
	// MinIO 流在 Hertz 刷出响应前关闭；内存 NopCloser 测试无法暴露该问题。
	writeAssetSafetyHeaders(c, content.ContentType, content.ETag)
	c.SetBodyStream(content.Body, int(content.SizeBytes))
}

// ContentHead 只返回元数据：授权规则与 GET 完全一致。
func (h *Asset) ContentHead(ctx context.Context, c *app.RequestContext) {
	assetID, ok := assetIDParam(c)
	if !ok {
		return
	}
	info, err := h.service.Stat(ctx, assetApp.ContentCommand{AssetID: assetID, ViewerUserID: h.viewerID(c)})
	if err != nil {
		h.reject(ctx, c, err)
		return
	}
	writeAssetSafetyHeaders(c, info.ContentType, info.ETag)
	c.Response.Header.SetContentLength(int(info.SizeBytes))
	c.Status(consts.StatusOK)
}

func (h *Asset) owner(c *app.RequestContext) (string, bool) {
	identity, ok := middleware.IdentityFrom(c)
	if !ok || identity.User.ID.IsZero() {
		presenter.WriteError(c, consts.StatusUnauthorized, presenter.CodeSessionInvalid, "登录状态无效，请重新登录", middleware.RequestIDFrom(c))
		return "", false
	}
	return identity.User.ID.String(), true
}

// viewerID 读取可选身份：匿名访问返回空字符串，由用例按公开引用判定。
func (h *Asset) viewerID(c *app.RequestContext) string {
	identity, ok := middleware.IdentityFrom(c)
	if !ok || identity.User.ID.IsZero() {
		return ""
	}
	return identity.User.ID.String()
}

func (h *Asset) idempotencyKey(ctx context.Context, c *app.RequestContext) (string, bool) {
	key, err := presenter.ParseIdempotencyKey(string(c.Request.Header.Peek(presenter.HeaderIdempotencyKey)))
	if err != nil {
		h.reject(ctx, c, err)
		return "", false
	}
	return key, true
}

func (h *Asset) reject(ctx context.Context, c *app.RequestContext, err error) {
	mapping := presenter.MapAssetError(err)
	if h.logger != nil {
		h.logger.InfoContext(ctx, "资产请求失败", "code", mapping.Code, "request_id", middleware.RequestIDFrom(c))
	}
	presenter.WriteMapping(c, mapping, middleware.RequestIDFrom(c))
}

// writeAssetSafetyHeaders 写出可信内容类型与安全响应头。
// Content-Type 取确认时探测出的类型：对象自带的类型来自上传方声明，不可直接回显。
func writeAssetSafetyHeaders(c *app.RequestContext, contentType, etag string) {
	if contentType != "" {
		c.Header("Content-Type", contentType)
	}
	if etag != "" {
		c.Header(presenter.HeaderETag, quoteETag(etag))
	}
	c.Header("X-Content-Type-Options", "nosniff")
	// 资产没有原始文件名，只按资产标识内联展示，避免任何用户可控的文件名进入响应头。
	c.Header("Content-Disposition", "inline")
}

// quoteETag 规范化对象存储返回的 ETag 为强 ETag 形式。
func quoteETag(value string) string {
	return `"` + strings.Trim(value, `"`) + `"`
}

func toArticleAsset(stored assetDomain.Asset) dto.ArticleAsset {
	result := dto.ArticleAsset{
		ID: stored.ID(), Status: string(stored.Status()),
		CreatedAt: stored.CreatedAt().UTC(), ConfirmedAt: utcTime(stored.ConfirmedAt()),
	}
	if media := stored.Media(); media != nil {
		contentType, size, width, height, checksum := media.ContentType, media.SizeBytes, media.Width, media.Height, media.Checksum
		result.ContentType, result.SizeBytes = &contentType, &size
		result.Width, result.Height = &width, &height
		result.Checksum = &checksum
	}
	return result
}

func assetIDParam(c *app.RequestContext) (string, bool) {
	assetID := strings.ToLower(strings.TrimSpace(c.Param("asset_id")))
	if !assetDomain.ValidID(assetID) {
		presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeValidationFailed, "资产 ID 无效", middleware.RequestIDFrom(c))
		return "", false
	}
	return assetID, true
}
