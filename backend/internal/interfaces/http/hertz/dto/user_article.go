package dto

import "time"

type CreateArticleRequest struct {
	Title         string `json:"title"`
	Markdown      string `json:"markdown"`
	InitialStatus string `json:"initial_status"`
}

// UpdateArticleRequest 只允许标题与正文，未知字段由严格解码拒绝。
type UpdateArticleRequest struct {
	Title    string `json:"title"`
	Markdown string `json:"markdown"`
}

type PreviewArticleRequest struct {
	Title    string `json:"title"`
	Markdown string `json:"markdown"`
}

// MyArticleSummary 是本人文章列表项：包含生命周期、修订号与乐观锁版本。
type MyArticleSummary struct {
	ID            int64      `json:"id"`
	Title         string     `json:"title"`
	Status        string     `json:"status"`
	OfflineReason *string    `json:"offline_reason"`
	RevisionNo    int        `json:"revision_no"`
	LockVersion   int64      `json:"lock_version"`
	PublishedAt   *time.Time `json:"published_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// MyArticleDetail 在摘要基础上携带作者私有内容：Markdown、清洗 HTML 与资产引用。
type MyArticleDetail struct {
	MyArticleSummary
	Markdown    string   `json:"markdown"`
	ContentHTML string   `json:"content_html"`
	Excerpt     string   `json:"excerpt"`
	AssetIDs    []string `json:"asset_ids"`
}

type MyArticlePage struct {
	Items      []MyArticleSummary `json:"items"`
	NextCursor *string            `json:"next_cursor"`
	HasMore    bool               `json:"has_more"`
}

// ArticlePreview 是无状态预览响应，不携带文章标识或版本。
type ArticlePreview struct {
	ContentHTML string   `json:"content_html"`
	PlainText   string   `json:"plain_text"`
	Excerpt     string   `json:"excerpt"`
	AssetIDs    []string `json:"asset_ids"`
}
