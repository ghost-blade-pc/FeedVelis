// Package dto 定义 HTTP 输入输出结构，不进入 Domain。
package dto

import "time"

type ArticleSource struct {
	ID      int64   `json:"id"`
	Title   string  `json:"title"`
	SiteURL *string `json:"site_url"`
}

// ArticleAuthor 只暴露稳定 ID 与昵称，不暴露登录用户名。
type ArticleAuthor struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
}

// RSSArticleOrigin 与 UserArticleOrigin 组成 origin 判别联合：ArticleItem.Origin 只会是其中之一。
type RSSArticleOrigin struct {
	Type              string        `json:"type"`
	Source            ArticleSource `json:"source"`
	CanonicalURL      string        `json:"canonical_url"`
	SourcePublishedAt *time.Time    `json:"source_published_at"`
}

type UserArticleOrigin struct {
	Type   string        `json:"type"`
	Author ArticleAuthor `json:"author"`
}

type ArticleItem struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Excerpt     string    `json:"excerpt"`
	PublishedAt time.Time `json:"published_at"`
	Origin      any       `json:"origin"`
}

type ArticleListResponse struct {
	Items      []ArticleItem `json:"items"`
	NextCursor *string       `json:"next_cursor"`
	HasMore    bool          `json:"has_more"`
}

// ArticleDetailResponse 在列表项基础上携带当前公开修订的清洗 HTML，字段平铺。
type ArticleDetailResponse struct {
	ArticleItem
	ContentHTML string `json:"content_html"`
}
