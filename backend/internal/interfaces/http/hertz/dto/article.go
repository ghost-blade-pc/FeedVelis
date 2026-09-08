// Package dto 定义 HTTP 输入输出结构，不进入 Domain。
package dto

import "time"

type ArticleSource struct {
	ID      int64   `json:"id"`
	Title   string  `json:"title"`
	SiteURL *string `json:"site_url"`
}

type ArticleItem struct {
	ID                int64         `json:"id"`
	Title             string        `json:"title"`
	CanonicalURL      string        `json:"canonical_url"`
	Source            ArticleSource `json:"source"`
	AuthorName        *string       `json:"author_name"`
	Excerpt           string        `json:"excerpt"`
	SourcePublishedAt *time.Time    `json:"source_published_at"`
	DiscoveredAt      time.Time     `json:"discovered_at"`
}

type ArticleListResponse struct {
	Items      []ArticleItem `json:"items"`
	NextCursor *string       `json:"next_cursor"`
	HasMore    bool          `json:"has_more"`
}

// ArticleDetailResponse 在列表项基础上携带清洗后的正文 HTML，字段平铺。
type ArticleDetailResponse struct {
	ArticleItem
	ContentHTML *string `json:"content_html"`
}
