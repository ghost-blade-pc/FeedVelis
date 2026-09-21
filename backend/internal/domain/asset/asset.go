// Package asset 定义私有文章图片的归属、生命周期、可引用性和匿名授权规则。
// 本包不依赖数据库、对象存储或 HTTP：对象键规则是纯字符串推导，媒体信息由调用方实测后注入。
package asset

import (
	"errors"
	"strings"
	"time"
)

// Status 是资产的生命周期状态，与迁移中的 CHECK 约束保持一致。
type Status string

const (
	StatusPending       Status = "pending"
	StatusReady         Status = "ready"
	StatusDeletePending Status = "delete_pending"
	StatusDeleted       Status = "deleted"
)

// 允许的图片类型；不接受其他类型，也不做转码。
const (
	ContentTypeJPEG = "image/jpeg"
	ContentTypePNG  = "image/png"
	ContentTypeWebP = "image/webp"
)

// ObjectKeyPrefix 是私有 Bucket 内所有文章资产的固定前缀。
const ObjectKeyPrefix = "article-assets"

const (
	maxChecksumBytes = 256
	maxObjectKeySize = 1024
)

var (
	ErrInvalidID      = errors.New("资产 ID 无效")
	ErrInvalidOwner   = errors.New("资产所有者无效")
	ErrInvalidKey     = errors.New("资产对象键无效")
	ErrInvalidStatus  = errors.New("资产状态不允许该操作")
	ErrForbidden      = errors.New("无权操作该资产")
	ErrNotReady       = errors.New("资产尚未确认")
	ErrBoundElsewhere = errors.New("资产已绑定其他文章")
	ErrInvalidMedia   = errors.New("资产媒体信息无效")
	ErrNotFound       = errors.New("资产不存在")
)

// Limits 是单文件与像素维度的上限；由配置注入，领域只负责执行。
type Limits struct {
	MaxFileBytes int64
	MaxWidth     int
	MaxHeight    int
	MaxPixels    int64
}

// Media 是对象存储实测得到的可信图片信息，只能由确认流程产生。
type Media struct {
	ContentType string
	SizeBytes   int64
	Width       int
	Height      int
	Checksum    string
}

// NewMedia 校验实测媒体信息；任何一项超限都拒绝确认。
func NewMedia(contentType string, sizeBytes int64, width, height int, checksum string, limits Limits) (Media, error) {
	switch contentType {
	case ContentTypeJPEG, ContentTypePNG, ContentTypeWebP:
	default:
		return Media{}, ErrInvalidMedia
	}
	if sizeBytes <= 0 || sizeBytes > limits.MaxFileBytes {
		return Media{}, ErrInvalidMedia
	}
	if width <= 0 || height <= 0 || width > limits.MaxWidth || height > limits.MaxHeight {
		return Media{}, ErrInvalidMedia
	}
	if int64(width)*int64(height) > limits.MaxPixels {
		return Media{}, ErrInvalidMedia
	}
	if len(checksum) > maxChecksumBytes {
		return Media{}, ErrInvalidMedia
	}
	return Media{ContentType: contentType, SizeBytes: sizeBytes, Width: width, Height: height, Checksum: checksum}, nil
}

// ObjectKey 由服务端按所有者与资产 ID 生成固定对象键，不使用原文件名。
func ObjectKey(ownerUserID, assetID string) (string, error) {
	owner, err := normalizeID(ownerUserID)
	if err != nil {
		return "", ErrInvalidOwner
	}
	identifier, err := normalizeID(assetID)
	if err != nil {
		return "", ErrInvalidID
	}
	key := ObjectKeyPrefix + "/" + owner + "/" + identifier + "/original"
	if len(key) > maxObjectKeySize {
		return "", ErrInvalidKey
	}
	return key, nil
}

// Asset 是私有图片资产聚合：身份与对象键不可变，状态经显式转换推进。
type Asset struct {
	id                string
	ownerUserID       string
	boundArticleID    *int64
	objectKey         string
	status            Status
	media             *Media
	quotaCountedAt    *time.Time
	confirmedAt       *time.Time
	deleteRequestedAt *time.Time
	deletedAt         *time.Time
	createdAt         time.Time
	updatedAt         time.Time
}

// NewPending 创建待上传资产；对象键立即固定，重复确认也不会改变它。
func NewPending(id, ownerUserID string, now time.Time) (Asset, error) {
	identifier, err := normalizeID(id)
	if err != nil {
		return Asset{}, err
	}
	key, err := ObjectKey(ownerUserID, identifier)
	if err != nil {
		return Asset{}, err
	}
	if now.IsZero() {
		return Asset{}, ErrInvalidStatus
	}
	return Asset{id: identifier, ownerUserID: strings.ToLower(ownerUserID), objectKey: key,
		status: StatusPending, createdAt: now.UTC(), updatedAt: now.UTC()}, nil
}

// Restore 从持久化数据重建聚合，不做校验转换：数据库约束已经保证取值合法。
func Restore(id, ownerUserID, objectKey string, status Status, boundArticleID *int64, media *Media,
	quotaCountedAt, confirmedAt, deleteRequestedAt, deletedAt *time.Time, createdAt, updatedAt time.Time) Asset {
	return Asset{id: id, ownerUserID: ownerUserID, objectKey: objectKey, status: status,
		boundArticleID: copyInt64(boundArticleID), media: copyMedia(media),
		quotaCountedAt: copyTime(quotaCountedAt), confirmedAt: copyTime(confirmedAt),
		deleteRequestedAt: copyTime(deleteRequestedAt), deletedAt: copyTime(deletedAt),
		createdAt: createdAt.UTC(), updatedAt: updatedAt.UTC()}
}

func (a Asset) ID() string                    { return a.id }
func (a Asset) OwnerUserID() string           { return a.ownerUserID }
func (a Asset) ObjectKey() string             { return a.objectKey }
func (a Asset) Status() Status                { return a.status }
func (a Asset) BoundArticleID() *int64        { return copyInt64(a.boundArticleID) }
func (a Asset) Media() *Media                 { return copyMedia(a.media) }
func (a Asset) QuotaCountedAt() *time.Time    { return copyTime(a.quotaCountedAt) }
func (a Asset) ConfirmedAt() *time.Time       { return copyTime(a.confirmedAt) }
func (a Asset) DeleteRequestedAt() *time.Time { return copyTime(a.deleteRequestedAt) }
func (a Asset) DeletedAt() *time.Time         { return copyTime(a.deletedAt) }
func (a Asset) CreatedAt() time.Time          { return a.createdAt }
func (a Asset) UpdatedAt() time.Time          { return a.updatedAt }

// OwnedBy 判断当前调用者是否是资产所有者。
func (a Asset) OwnedBy(actorUserID string) bool {
	return a.ownerUserID != "" && a.ownerUserID == strings.ToLower(strings.TrimSpace(actorUserID))
}

// Confirm 把待上传资产推进为可引用状态。
// 只有所有者可以确认，且媒体信息必须通过限制校验；重复确认同一内容保持幂等。
func (a *Asset) Confirm(actorUserID string, media Media, now time.Time) error {
	if !a.OwnedBy(actorUserID) {
		return ErrForbidden
	}
	switch a.status {
	case StatusReady:
		return nil
	case StatusPending:
	default:
		return ErrInvalidStatus
	}
	if now.IsZero() {
		return ErrInvalidStatus
	}
	confirmed := now.UTC()
	a.status = StatusReady
	a.media = copyMedia(&media)
	a.confirmedAt = &confirmed
	a.updatedAt = confirmed
	return nil
}

// Bind 把资产绑定到一篇文章；已绑定其他文章时拒绝，绑定同一文章时保持幂等。
func (a *Asset) Bind(articleID int64, now time.Time) error {
	if articleID <= 0 {
		return ErrBoundElsewhere
	}
	if a.status != StatusReady {
		return ErrNotReady
	}
	if a.boundArticleID != nil {
		if *a.boundArticleID == articleID {
			return nil
		}
		return ErrBoundElsewhere
	}
	if now.IsZero() {
		return ErrInvalidStatus
	}
	bound := articleID
	a.boundArticleID = &bound
	a.updatedAt = now.UTC()
	return nil
}

// RequestDelete 标记对象待删除；下架不是删除，只有显式请求才进入该状态。
func (a *Asset) RequestDelete(now time.Time) error {
	switch a.status {
	case StatusPending, StatusReady:
	case StatusDeletePending, StatusDeleted:
		return nil
	default:
		return ErrInvalidStatus
	}
	if now.IsZero() {
		return ErrInvalidStatus
	}
	requested := now.UTC()
	a.status = StatusDeletePending
	a.deleteRequestedAt = &requested
	a.updatedAt = requested
	return nil
}

// MarkDeleted 记录对象已被对象存储删除；重复标记保持幂等。
func (a *Asset) MarkDeleted(now time.Time) error {
	if a.status == StatusDeleted {
		return nil
	}
	if a.status != StatusDeletePending || now.IsZero() {
		return ErrInvalidStatus
	}
	deleted := now.UTC()
	a.status = StatusDeleted
	a.deletedAt = &deleted
	a.updatedAt = deleted
	return nil
}

// MarkQuotaCounted 记录额度已计入；由仓储在行锁内调用，保证同一资产只计一次。
func (a *Asset) MarkQuotaCounted(now time.Time) bool {
	if a.quotaCountedAt != nil || now.IsZero() {
		return false
	}
	counted := now.UTC()
	a.quotaCountedAt = &counted
	return true
}

// ValidID 判断给定文本是否是规范 UUID；接口层用它把非法标识挡在用例之外。
func ValidID(value string) bool {
	_, err := normalizeID(value)
	return err == nil
}

// normalizeID 只接受规范 UUID 文本，并统一为小写，避免同一资产出现两种身份写法。
func normalizeID(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if len(normalized) != 36 {
		return "", ErrInvalidID
	}
	for index, char := range normalized {
		switch index {
		case 8, 13, 18, 23:
			if char != '-' {
				return "", ErrInvalidID
			}
		default:
			if !isHexDigit(char) {
				return "", ErrInvalidID
			}
		}
	}
	return normalized, nil
}

func isHexDigit(char rune) bool {
	return (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')
}

func copyInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func copyMedia(value *Media) *Media {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func copyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
