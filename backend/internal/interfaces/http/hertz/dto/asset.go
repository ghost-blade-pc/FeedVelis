package dto

import "time"

// CreateAssetRequest 只接受类型与大小声明，未知字段由严格解码拒绝。
type CreateAssetRequest struct {
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

// ArticleAsset 是资产的对外视图：对象键与所有者都不对外暴露。
type ArticleAsset struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	ContentType *string    `json:"content_type"`
	SizeBytes   *int64     `json:"size_bytes"`
	Width       *int       `json:"width"`
	Height      *int       `json:"height"`
	Checksum    *string    `json:"checksum"`
	CreatedAt   time.Time  `json:"created_at"`
	ConfirmedAt *time.Time `json:"confirmed_at"`
}

// AssetUpload 是创建资产的结果：资产身份加一次性直传信息。
type AssetUpload struct {
	Asset         ArticleAsset      `json:"asset"`
	UploadURL     string            `json:"upload_url"`
	UploadMethod  string            `json:"upload_method"`
	UploadHeaders map[string]string `json:"upload_headers"`
	ExpiresAt     time.Time         `json:"expires_at"`
}
