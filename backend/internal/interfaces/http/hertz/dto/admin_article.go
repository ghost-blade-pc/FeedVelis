package dto

import "time"

// AdminArticleResult 是管理员下架/恢复的结果：只暴露可见性相关字段，不含正文或草稿信息。
type AdminArticleResult struct {
	ID            int64     `json:"id"`
	Status        string    `json:"status"`
	OfflineReason *string   `json:"offline_reason"`
	LockVersion   int64     `json:"lock_version"`
	PublishedAt   time.Time `json:"published_at"`
}
