// Package articlefeedback 定义文章反馈的时间与状态规则。
package articlefeedback

import (
	"errors"
	"time"
)

var ErrInvalidInput = errors.New("反馈参数无效")
var ErrArticleNotFound = errors.New("文章不存在")

const ReadRetention = 90 * 24 * time.Hour
const NotInterestedRetention = 180 * 24 * time.Hour

// ReadWindow 将服务端时间对齐到 UTC 的半小时固定窗口。
func ReadWindow(now time.Time) time.Time {
	return now.UTC().Truncate(30 * time.Minute)
}

// ReadDay 将服务端时间对齐到 UTC 自然日。
func ReadDay(now time.Time) time.Time {
	year, month, day := now.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

type State struct {
	ArticleID              int64      `json:"article_id"`
	Favorited              bool       `json:"favorited"`
	NotInterested          bool       `json:"not_interested"`
	NotInterestedExpiresAt *time.Time `json:"not_interested_expires_at"`
}
