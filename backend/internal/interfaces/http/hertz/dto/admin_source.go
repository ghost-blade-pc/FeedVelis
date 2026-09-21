package dto

import "time"

// CreateSourceRequest 只接受 URL 与可选周期；周期以秒为单位，与数据库列一致。
type CreateSourceRequest struct {
	FeedURL              string `json:"feed_url"`
	FetchIntervalSeconds *int64 `json:"fetch_interval_seconds"`
}

// UpdateSourceRequest 只允许修改抓取周期：Feed URL 创建后不可修改。
type UpdateSourceRequest struct {
	FetchIntervalSeconds int64 `json:"fetch_interval_seconds"`
}

// AdminSource 是来源的对外视图；Feed URL 不可修改，因此不提供任何改写入口。
type AdminSource struct {
	ID                   int64      `json:"id"`
	FeedURL              string     `json:"feed_url"`
	Title                string     `json:"title"`
	Status               string     `json:"status"`
	FetchIntervalSeconds int64      `json:"fetch_interval_seconds"`
	LockVersion          int64      `json:"lock_version"`
	NextFetchAt          *time.Time `json:"next_fetch_at"`
	LastSuccessAt        *time.Time `json:"last_success_at"`
	ConsecutiveFailures  int        `json:"consecutive_failures"`
	LastErrorCode        *string    `json:"last_error_code"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type SourcePage struct {
	Items      []AdminSource `json:"items"`
	NextCursor *string       `json:"next_cursor"`
	HasMore    bool          `json:"has_more"`
}

// SourceFetchRun 是抓取历史条目：只包含计数与受控错误码，不含任何响应正文。
type SourceFetchRun struct {
	ID             string     `json:"id"`
	SourceID       int64      `json:"source_id"`
	Trigger        string     `json:"trigger"`
	Status         string     `json:"status"`
	NotModified    bool       `json:"not_modified"`
	InsertedCount  int        `json:"inserted_count"`
	UpdatedCount   int        `json:"updated_count"`
	UnchangedCount int        `json:"unchanged_count"`
	SkippedCount   int        `json:"skipped_count"`
	ErrorCode      *string    `json:"error_code"`
	StartedAt      time.Time  `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at"`
}

type SourceFetchRunPage struct {
	Items      []SourceFetchRun `json:"items"`
	NextCursor *string          `json:"next_cursor"`
	HasMore    bool             `json:"has_more"`
}
