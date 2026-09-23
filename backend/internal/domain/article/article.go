// Package article 定义外部文章身份、内容版本与列表读取模型。
package article

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/shared"
)

const (
	MaxSourceItemIDBytes    = 2048
	MaxCanonicalURLBytes    = 4096
	MaxTitleRunes           = 500
	MaxAuthorRunes          = 300
	MaxExcerptRunes         = 1000
	MaxRawDescriptionBytes  = 256 * 1024
	MaxRawContentBytes      = 1024 * 1024
	CurrentSanitizerVersion = 2 // v2：清洗保留图片（img/figure/figcaption）等富文本结构
)

var (
	ErrMissingIdentity = errors.New("文章缺少稳定身份")
	ErrInvalidURL      = errors.New("文章原文 URL 无效")
	ErrInvalidCursor   = errors.New("文章游标无效")
	ErrInvalidArgument = errors.New("文章列表参数无效")
	ErrNotFound        = errors.New("文章不存在或不可见")
)

type Status string

// RSS 入库只产生 published 文章；下架与删除属于统一聚合的生命周期，由 aggregate.go 定义。
const StatusPublished Status = "published"

type Article struct {
	ID                int64
	SourceID          int64
	DedupeKey         string
	SourceItemID      *string
	CanonicalURL      string
	Title             string
	AuthorName        *string
	Excerpt           string
	Language          string
	SourcePublishedAt *time.Time
	DiscoveredAt      time.Time
	SourceUpdatedAt   *time.Time
	ContentHash       string
	Status            Status
	LastSeenAt        time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type Content struct {
	ArticleID               int64
	RawDescription          *string
	RawContent              *string
	RawDescriptionTruncated bool
	RawContentTruncated     bool
	SanitizedHTML           *string
	PlainText               string
	SanitizerVersion        int
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

type Candidate struct {
	Article Article
	Content Content
}

type UpsertResult string

const (
	UpsertInserted  UpsertResult = "inserted"
	UpsertUpdated   UpsertResult = "updated"
	UpsertUnchanged UpsertResult = "unchanged"
)

// MutationResult 是 RSS 写入后的数据库当前事实，供 Application 在同一事务构造集成事件。
type MutationResult struct {
	Result      UpsertResult
	ArticleID   int64
	Origin      OriginType
	RevisionID  int64
	RevisionNo  int
	ContentHash string
	Status      Status
	LockVersion int64
}

type MutationRepository interface {
	UpsertMutation(context.Context, Candidate, time.Time) (MutationResult, error)
}

type SourceSummary struct {
	ID      int64
	Title   string
	SiteURL *string
}

type AuthorSummary struct {
	ID       string
	Nickname string
}

type ListItem struct {
	ID                int64
	Origin            OriginType
	Title             string
	CanonicalURL      string
	Source            SourceSummary
	AuthorName        *string
	Excerpt           string
	SourcePublishedAt *time.Time
	DiscoveredAt      time.Time
	SortAt            time.Time
	Author            *AuthorSummary
}

type Cursor struct {
	SortAt    time.Time
	ArticleID int64
}

// Detail 是单篇文章的完整读取模型：列表元数据 + 清洗后的正文 HTML。
type Detail struct {
	Item          ListItem
	SanitizedHTML *string
}

type Repository interface {
	Upsert(context.Context, Candidate, time.Time) (UpsertResult, int64, error)
	ListPublished(context.Context, *Cursor, int) ([]ListItem, error)
	GetPublished(context.Context, int64) (Detail, error)
}

func DedupeKey(sourceItemID *string, canonicalURL string) (string, error) {
	if sourceItemID != nil {
		id := strings.TrimSpace(*sourceItemID)
		if id != "" {
			return digest("id\x00" + id), nil
		}
	}
	if canonicalURL == "" {
		return "", ErrMissingIdentity
	}
	normalized, err := NormalizeCanonicalURL(canonicalURL)
	if err != nil {
		return "", err
	}
	return digest("url\x00" + normalized), nil
}

func NormalizeCanonicalURL(raw string) (string, error) {
	value, err := shared.NormalizeHTTPURL(raw, MaxCanonicalURLBytes)
	if err != nil {
		return "", ErrInvalidURL
	}
	return value, nil
}

func NormalizeText(value string) string {
	value = strings.ToValidUTF8(value, "�")
	value = strings.ReplaceAll(value, "\x00", "�")
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.TrimSpace(value)
}

func TruncateRunes(value string, limit int) string {
	runes := []rune(NormalizeText(value))
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}

func TruncateUTF8Bytes(value string, limit int) (string, bool) {
	value = strings.ToValidUTF8(value, "�")
	value = strings.ReplaceAll(value, "\x00", "�")
	if len(value) <= limit {
		return value, false
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value, true
}

func ContentHash(a Article, c Content) string {
	published := ""
	if a.SourcePublishedAt != nil {
		published = a.SourcePublishedAt.UTC().Format(time.RFC3339Nano)
	}
	payload, _ := json.Marshal([]string{
		NormalizeText(a.Title),
		a.CanonicalURL,
		stringValue(a.AuthorName),
		NormalizeText(a.Language),
		published,
		stringValue(c.RawDescription),
		strconv.FormatBool(c.RawDescriptionTruncated),
		stringValue(c.RawContent),
		strconv.FormatBool(c.RawContentTruncated),
		// 清洗器版本参与哈希：升级后同一原始内容会触发重洗
		strconv.Itoa(c.SanitizerVersion),
	})
	return digest(string(payload))
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return NormalizeText(*value)
}
