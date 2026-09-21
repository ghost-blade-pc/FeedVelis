// Package asset 编排私有图片资产的上传与确认：先建待上传身份并签发短期直传凭证，
// 再由确认流程以对象存储实测结果为准推进为可引用状态并计入额度。
package asset

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"

	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	assetDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/asset"
)

var (
	// ErrQuotaExceeded 表示确认后会超出用户资产总额度。
	ErrQuotaExceeded = errors.New("资产额度不足")
	// ErrPendingLimitExceeded 表示待确认资产数量已达上限。
	ErrPendingLimitExceeded = errors.New("待确认资产过多")
)

// Policy 是资产能力的运行时约束；由配置注入，应用层只负责执行。
type Policy struct {
	Limits         assetDomain.Limits
	UserQuotaBytes int64
	PendingLimit   int
	UploadTTL      time.Duration
	// ProbeBytes 是识别图片签名与尺寸允许读取的最大字节数。
	ProbeBytes int64
}

type Service struct {
	repository  assetDomain.Repository
	storage     ports.AssetStorage
	idempotency *idempotencyApp.Service
	clock       ports.Clock
	policy      Policy
}

func NewService(repository assetDomain.Repository, storage ports.AssetStorage,
	idempotency *idempotencyApp.Service, clock ports.Clock, policy Policy) *Service {
	return &Service{repository: repository, storage: storage, idempotency: idempotency, clock: clock, policy: policy}
}

// Upload 是创建资产的结果：资产身份加上一次性直传信息。
type Upload struct {
	Asset     assetDomain.Asset
	URL       string
	Method    string
	Headers   map[string]string
	ExpiresAt time.Time
}

type CreateCommand struct {
	OwnerUserID    string
	IdempotencyKey string
	// ContentType 与 SizeBytes 是客户端声明值，只用于尽早拒绝明显越界的请求；
	// 确认以对象存储实测结果为准。
	ContentType string
	SizeBytes   int64
}

func (s *Service) Create(ctx context.Context, command CreateCommand) (Upload, bool, error) {
	if !supportedContentType(command.ContentType) {
		return Upload{}, false, assetDomain.ErrInvalidMedia
	}
	if command.SizeBytes <= 0 || command.SizeBytes > s.policy.Limits.MaxFileBytes {
		return Upload{}, false, assetDomain.ErrInvalidMedia
	}
	payload := struct {
		ContentType string `json:"content_type"`
		SizeBytes   int64  `json:"size_bytes"`
	}{command.ContentType, command.SizeBytes}
	now := s.clock.Now().UTC()
	outcome, err := s.idempotency.Execute(ctx, idempotencyApp.Command{
		Identity: idempotencyApp.Identity{ActorUserID: command.OwnerUserID, Operation: "asset.create", Key: command.IdempotencyKey},
		Payload:  payload, Now: now,
	}, func(txContext context.Context) (any, string, string, error) {
		usage, usageErr := s.repository.Usage(txContext, command.OwnerUserID)
		if usageErr != nil {
			return nil, "", "", usageErr
		}
		if usage.PendingCount >= s.policy.PendingLimit {
			return nil, "", "", ErrPendingLimitExceeded
		}
		// 创建时用声明大小做保守预检，确认时再以实测大小复核。
		if usage.CountedBytes+command.SizeBytes > s.policy.UserQuotaBytes {
			return nil, "", "", ErrQuotaExceeded
		}
		pending, pendingErr := assetDomain.NewPending(uuid.NewString(), command.OwnerUserID, now)
		if pendingErr != nil {
			return nil, "", "", pendingErr
		}
		if createErr := s.repository.Create(txContext, pending); createErr != nil {
			return nil, "", "", createErr
		}
		// 事务内只固化资产身份；直传凭证在事务外签发，使首次与重放走同一条路径。
		return pending.ID(), "asset", pending.ID(), nil
	})
	if err != nil {
		return Upload{}, false, err
	}
	assetID, err := decodeAssetID(outcome.Result)
	if err != nil {
		return Upload{}, false, err
	}
	stored, err := s.repository.Get(ctx, assetID)
	if err != nil {
		return Upload{}, false, err
	}
	// 归属校验在这里同样生效：幂等结果只保存标识，不保存可篡改的资产内容。
	if !stored.OwnedBy(command.OwnerUserID) {
		return Upload{}, false, assetDomain.ErrNotFound
	}
	// 预签名地址有时效，重放时必须重新签发而不是复用首次结果。
	presigned, err := s.storage.PresignUpload(ctx, stored.ObjectKey(), s.policy.UploadTTL)
	if err != nil {
		return Upload{}, false, err
	}
	return Upload{Asset: stored, URL: presigned.URL, Method: presigned.Method,
		Headers: presigned.Headers, ExpiresAt: now.Add(s.policy.UploadTTL)}, outcome.Replayed, nil
}

type ConfirmCommand struct {
	OwnerUserID    string
	AssetID        string
	IdempotencyKey string
}

// Confirm 以对象存储实测结果确认资产：先 HEAD 取真实大小，再在受限预算内识别签名与尺寸，
// 最后在所有者行锁内复核额度并只计入一次。
func (s *Service) Confirm(ctx context.Context, command ConfirmCommand) (assetDomain.Asset, bool, error) {
	payload := struct {
		AssetID string `json:"asset_id"`
	}{command.AssetID}
	now := s.clock.Now().UTC()
	outcome, err := s.idempotency.Execute(ctx, idempotencyApp.Command{
		Identity: idempotencyApp.Identity{ActorUserID: command.OwnerUserID, Operation: "asset.confirm", Key: command.IdempotencyKey},
		Payload:  payload, Now: now,
	}, func(txContext context.Context) (any, string, string, error) {
		stored, confirmErr := s.confirm(txContext, command, now)
		if confirmErr != nil {
			return nil, "", "", confirmErr
		}
		return stored.ID(), "asset", stored.ID(), nil
	})
	if err != nil {
		return assetDomain.Asset{}, false, err
	}
	assetID, err := decodeAssetID(outcome.Result)
	if err != nil {
		return assetDomain.Asset{}, false, err
	}
	stored, err := s.repository.Get(ctx, assetID)
	if err != nil {
		return assetDomain.Asset{}, false, err
	}
	return stored, outcome.Replayed, nil
}

func (s *Service) confirm(ctx context.Context, command ConfirmCommand, now time.Time) (assetDomain.Asset, error) {
	stored, err := s.repository.Get(ctx, command.AssetID)
	if err != nil {
		return assetDomain.Asset{}, err
	}
	// 非所有者一律按不存在处理，不泄露他人资产的私有元数据。
	if !stored.OwnedBy(command.OwnerUserID) {
		return assetDomain.Asset{}, assetDomain.ErrNotFound
	}
	switch stored.Status() {
	case assetDomain.StatusReady:
		// 重复确认保持幂等：不再探测、不再计费。
		return stored, nil
	case assetDomain.StatusPending:
	default:
		// 已进入删除流程的资产等同于不存在。
		return assetDomain.Asset{}, assetDomain.ErrNotFound
	}

	// HEAD 先给出真实大小，避免对超限对象做任何探测读取。
	info, err := s.storage.StatObject(ctx, stored.ObjectKey())
	if errors.Is(err, ports.ErrObjectNotFound) {
		return assetDomain.Asset{}, ErrObjectMissing
	}
	if err != nil {
		return assetDomain.Asset{}, err
	}
	if info.SizeBytes <= 0 || info.SizeBytes > s.policy.Limits.MaxFileBytes {
		return assetDomain.Asset{}, assetDomain.ErrInvalidMedia
	}
	probe, err := s.storage.ProbeImage(ctx, stored.ObjectKey(), s.policy.ProbeBytes)
	if err != nil {
		return assetDomain.Asset{}, err
	}
	// 校验值取对象存储自身的内容标识：确认流程不额外整包读取对象。
	media, err := assetDomain.NewMedia(probe.ContentType, info.SizeBytes, probe.Width, probe.Height, info.ETag, s.policy.Limits)
	if err != nil {
		return assetDomain.Asset{}, err
	}
	if err := stored.Confirm(command.OwnerUserID, media, now); err != nil {
		return assetDomain.Asset{}, err
	}
	// 行锁内复核额度：并发的不同资产确认不会一起穿透总量上限。
	if err := s.repository.LockOwner(ctx, command.OwnerUserID); err != nil {
		return assetDomain.Asset{}, err
	}
	usage, err := s.repository.Usage(ctx, command.OwnerUserID)
	if err != nil {
		return assetDomain.Asset{}, err
	}
	if usage.CountedBytes+media.SizeBytes > s.policy.UserQuotaBytes {
		return assetDomain.Asset{}, ErrQuotaExceeded
	}
	stored.MarkQuotaCounted(now)
	if err := s.repository.Save(ctx, stored); err != nil {
		return assetDomain.Asset{}, err
	}
	return stored, nil
}

// ContentCommand 描述一次图片读取；ViewerUserID 为空表示匿名访问。
type ContentCommand struct {
	AssetID      string
	ViewerUserID string
}

// Content 是流式读取结果。调用方必须关闭 Body，且不得把完整对象缓冲进内存。
type Content struct {
	Body        io.ReadCloser
	SizeBytes   int64
	ContentType string
	ETag        string
}

// Open 授权后打开对象字节流：作者可预览自己的资产，其他人只能读到当前公开修订引用的资产。
func (s *Service) Open(ctx context.Context, command ContentCommand) (Content, error) {
	stored, err := s.authorize(ctx, command)
	if err != nil {
		return Content{}, err
	}
	stream, err := s.storage.OpenObject(ctx, stored.ObjectKey())
	if err != nil {
		return Content{}, err
	}
	return Content{
		Body: stream.Body, SizeBytes: stream.SizeBytes,
		ContentType: trustedContentType(stored, stream.ContentType), ETag: stream.ETag,
	}, nil
}

// Stat 只读取对象元数据，供 HEAD 使用；授权规则与 Open 相同。
func (s *Service) Stat(ctx context.Context, command ContentCommand) (ports.ObjectInfo, error) {
	stored, err := s.authorize(ctx, command)
	if err != nil {
		return ports.ObjectInfo{}, err
	}
	info, err := s.storage.StatObject(ctx, stored.ObjectKey())
	if err != nil {
		return ports.ObjectInfo{}, err
	}
	info.ContentType = trustedContentType(stored, info.ContentType)
	return info, nil
}

// authorize 一次查询判定“当前身份是所有者”或“资产被当前公开修订引用”；
// 其余情况一律按不存在处理，不下发任何私有元数据。
func (s *Service) authorize(ctx context.Context, command ContentCommand) (assetDomain.Asset, error) {
	stored, err := s.repository.Get(ctx, command.AssetID)
	if err != nil {
		return assetDomain.Asset{}, err
	}
	// 只有已确认资产可能被授权：待确认、待删除与已删除都不授权读取。
	if stored.Status() != assetDomain.StatusReady {
		return assetDomain.Asset{}, assetDomain.ErrNotFound
	}
	if command.ViewerUserID != "" && stored.OwnedBy(command.ViewerUserID) {
		return stored, nil
	}
	referenced, err := s.repository.IsPubliclyReferenced(ctx, stored.ID())
	if err != nil {
		return assetDomain.Asset{}, err
	}
	if !referenced {
		return assetDomain.Asset{}, assetDomain.ErrNotFound
	}
	return stored, nil
}

// trustedContentType 以确认时探测出的类型为准：对象自带的 Content-Type 来自上传方声明。
func trustedContentType(stored assetDomain.Asset, fallback string) string {
	if media := stored.Media(); media != nil {
		return media.ContentType
	}
	return fallback
}

// ErrObjectMissing 表示确认时对象尚未上传：这是客户端问题，不是存储故障。
var ErrObjectMissing = errors.New("资产对象尚未上传")

// IsObjectMissing 供接口层区分“对象还没上传”和“存储不可用”。
func IsObjectMissing(err error) bool { return errors.Is(err, ErrObjectMissing) }

// decodeAssetID 还原幂等结果：记录里只保存资产标识，聚合内容始终从数据库现读，
// 避免把领域对象序列化进幂等载荷，也避免重放返回过期状态。
func decodeAssetID(raw json.RawMessage) (string, error) {
	var assetID string
	if err := json.Unmarshal(raw, &assetID); err != nil {
		return "", err
	}
	if assetID == "" {
		return "", errors.New("幂等结果缺少资产标识")
	}
	return assetID, nil
}

func supportedContentType(value string) bool {
	switch value {
	case assetDomain.ContentTypeJPEG, assetDomain.ContentTypePNG, assetDomain.ContentTypeWebP:
		return true
	default:
		return false
	}
}
