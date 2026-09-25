package searchprojection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// fingerprintPayload 是投影内容指纹的稳定输入：字段顺序由结构体固定，
// 可选字段用空值参与，保证同一事实在不同进程与索引间得到同一指纹。
type fingerprintPayload struct {
	ArticleID          int64  `json:"article_id"`
	Generation         int64  `json:"projection_generation"`
	LockVersion        int64  `json:"lock_version"`
	RevisionID         int64  `json:"revision_id"`
	SchemaVersion      int    `json:"schema_version"`
	Visible            bool   `json:"visible"`
	InvisibleReason    string `json:"invisible_reason"`
	OriginType         string `json:"origin_type"`
	SourceID           int64  `json:"source_id"`
	AuthorUserID       string `json:"author_user_id"`
	PublishedAt        string `json:"published_at"`
	SourceTitle        string `json:"source_title"`
	AuthorName         string `json:"author_name"`
	Title              string `json:"title"`
	PlainText          string `json:"plain_text"`
	Excerpt            string `json:"excerpt"`
	Summary            string `json:"summary"`
	Keywords           string `json:"keywords"`
	Topics             string `json:"topics"`
	GenerationResultID string `json:"generation_result_id"`
	EmbeddingResultID  string `json:"embedding_result_id"`
	Vector             string `json:"vector"`
}

// DocumentFingerprint 计算投影内容指纹，供重建校验比对身份与内容。
// 它只覆盖进入搜索文档的字段，不包含 Markdown、清洗 HTML 或模型审计。
func DocumentFingerprint(document Document) string {
	sourceID := int64(0)
	if document.SourceID != nil {
		sourceID = *document.SourceID
	}
	publishedAt := ""
	if document.PublishedAt != nil {
		publishedAt = document.PublishedAt.UTC().Format(time.RFC3339Nano)
	}
	payload := fingerprintPayload{
		ArticleID: document.ArticleID, Generation: document.Generation, LockVersion: document.LockVersion,
		RevisionID: document.RevisionID, SchemaVersion: document.SchemaVersion, Visible: document.Visible,
		InvisibleReason: document.InvisibleReason, OriginType: document.OriginType, SourceID: sourceID,
		AuthorUserID: document.AuthorUserID, PublishedAt: publishedAt, SourceTitle: document.SourceTitle,
		AuthorName: document.AuthorName, Title: document.Title, PlainText: document.PlainText,
		Excerpt: document.Excerpt, Summary: document.Summary,
		Keywords: canonicalList(document.Keywords), Topics: canonicalList(document.Topics),
		GenerationResultID: document.GenerationResultID, EmbeddingResultID: document.EmbeddingResultID,
		Vector: canonicalVector(document.Vector),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func canonicalList(values []string) string {
	encoded, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func canonicalVector(values []float64) string {
	encoded, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(encoded)
}
