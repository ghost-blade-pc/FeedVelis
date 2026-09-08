// Package article 定义外部文章身份、内容版本与列表读取模型。
package article

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	CurrentSanitizerVersion = 1
)

var (
	ErrMissingIdentity = errors.New("文章缺少稳定身份")
	ErrInvalidURL      = errors.New("文章原文 URL 无效")
	ErrInvalidCursor   = errors.New("文章游标无效")
	ErrInvalidArgument = errors.New("文章列表参数无效")
)

type Status string

const (
	StatusPublished Status = "published"
	StatusHidden    Status = "hidden"
)

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

type SourceSummary struct {
	ID      int64
	Title   string
	SiteURL *string
}

type ListItem struct {
	ID                int64
	Title             string
	CanonicalURL      string
	Source            SourceSummary
	AuthorName        *string
	Excerpt           string
	SourcePublishedAt *time.Time
	DiscoveredAt      time.Time
	SortAt            time.Time
}

type Cursor struct {
	SortAt    time.Time
	ArticleID int64
}

type Repository interface {
	Upsert(context.Context, Candidate, time.Time) (UpsertResult, int64, error)
	ListPublished(context.Context, *Cursor, int) ([]ListItem, error)
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
		stringValue(c.RawContent),
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
