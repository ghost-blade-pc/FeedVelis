// Package article 编排外部条目入库和文章列表用例。
package article

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type Service struct {
	repository articleDomain.Repository
	sanitizer  ports.ContentSanitizer
	clock      ports.Clock
	txManager  ports.TxManager
}

type IngestReport struct {
	Inserted  int
	Updated   int
	Unchanged int
	Skipped   map[string]int
}

type Page struct {
	Items      []articleDomain.ListItem
	NextCursor *string
	HasMore    bool
}

type cursorPayload struct {
	Version   int       `json:"v"`
	SortAt    time.Time `json:"sort_at"`
	ArticleID int64     `json:"article_id"`
}

func NewService(repository articleDomain.Repository, sanitizer ports.ContentSanitizer, clock ports.Clock, txManagers ...ports.TxManager) *Service {
	service := &Service{repository: repository, sanitizer: sanitizer, clock: clock}
	if len(txManagers) > 0 {
		service.txManager = txManagers[0]
	}
	return service
}

func (s *Service) Ingest(ctx context.Context, sourceID int64, items []ports.ParsedItem) (IngestReport, error) {
	report := IngestReport{Skipped: make(map[string]int)}
	now := s.clock.Now().UTC()
	for _, item := range items {
		candidate, reason, err := s.candidate(sourceID, item, now)
		if err != nil {
			return report, err
		}
		if reason != "" {
			report.Skipped[reason]++
			continue
		}
		var result articleDomain.UpsertResult
		if s.txManager == nil {
			result, _, err = s.repository.Upsert(ctx, candidate, now)
		} else {
			err = s.txManager.WithinTransaction(ctx, func(txContext context.Context) error {
				var transactionErr error
				result, _, transactionErr = s.repository.Upsert(txContext, candidate, now)
				return transactionErr
			})
		}
		if err != nil {
			return report, err
		}
		switch result {
		case articleDomain.UpsertInserted:
			report.Inserted++
		case articleDomain.UpsertUpdated:
			report.Updated++
		case articleDomain.UpsertUnchanged:
			report.Unchanged++
		}
	}
	return report, nil
}

func (s *Service) candidate(sourceID int64, item ports.ParsedItem, now time.Time) (articleDomain.Candidate, string, error) {
	canonicalURL, err := articleDomain.NormalizeCanonicalURL(item.URL)
	if err != nil {
		return articleDomain.Candidate{}, "INVALID_CANONICAL_URL", nil
	}
	key, err := articleDomain.DedupeKey(item.ID, canonicalURL)
	if errors.Is(err, articleDomain.ErrMissingIdentity) {
		return articleDomain.Candidate{}, "MISSING_IDENTITY", nil
	}
	if err != nil {
		return articleDomain.Candidate{}, "INVALID_IDENTITY", nil
	}
	title := articleDomain.TruncateRunes(item.Title, articleDomain.MaxTitleRunes)
	if title == "" {
		title = "未命名文章"
	}
	language := articleDomain.TruncateRunes(item.Language, 16)
	if language == "" {
		language = "und"
	}
	author := normalizedOptional(item.AuthorName, articleDomain.MaxAuthorRunes)
	sourceItemID := optionalBytes(item.ID, articleDomain.MaxSourceItemIDBytes)
	rawDescription, descriptionTruncated := truncatedOptional(item.Description, articleDomain.MaxRawDescriptionBytes)
	rawContent, contentTruncated := truncatedOptional(item.Content, articleDomain.MaxRawContentBytes)
	selected := ""
	if rawContent != nil && *rawContent != "" {
		selected = *rawContent
	} else if rawDescription != nil {
		selected = *rawDescription
	}
	sanitized := s.sanitizer.Sanitize(selected)
	sanitizedHTML := optionalString(sanitized.HTML)
	a := articleDomain.Article{
		SourceID: sourceID, DedupeKey: key, SourceItemID: sourceItemID,
		CanonicalURL: canonicalURL, Title: title, AuthorName: author,
		Excerpt:  articleDomain.TruncateRunes(sanitized.PlainText, articleDomain.MaxExcerptRunes),
		Language: language, SourcePublishedAt: utcPointer(item.PublishedAt),
		DiscoveredAt: now, SourceUpdatedAt: utcPointer(item.UpdatedAt),
		Status: articleDomain.StatusPublished, LastSeenAt: now,
	}
	c := articleDomain.Content{
		RawDescription: rawDescription, RawContent: rawContent,
		RawDescriptionTruncated: descriptionTruncated, RawContentTruncated: contentTruncated,
		SanitizedHTML: sanitizedHTML, PlainText: sanitized.PlainText,
		SanitizerVersion: articleDomain.CurrentSanitizerVersion,
	}
	a.ContentHash = articleDomain.ContentHash(a, c)
	return articleDomain.Candidate{Article: a, Content: c}, "", nil
}

// Get 读取单篇已发布文章的元数据与清洗后的正文 HTML。
func (s *Service) Get(ctx context.Context, articleID int64) (articleDomain.Detail, error) {
	if articleID <= 0 {
		return articleDomain.Detail{}, articleDomain.ErrInvalidArgument
	}
	return s.repository.GetPublished(ctx, articleID)
}

func (s *Service) List(ctx context.Context, encodedCursor string, limit int) (Page, error) {
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 50 {
		return Page{}, articleDomain.ErrInvalidArgument
	}
	var cursor *articleDomain.Cursor
	if encodedCursor != "" {
		decoded, err := decodeCursor(encodedCursor)
		if err != nil {
			return Page{}, err
		}
		cursor = &decoded
	}
	items, err := s.repository.ListPublished(ctx, cursor, limit+1)
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.HasMore = true
		last := page.Items[len(page.Items)-1]
		encoded := encodeCursor(articleDomain.Cursor{SortAt: last.SortAt, ArticleID: last.ID})
		page.NextCursor = &encoded
	}
	return page, nil
}

func encodeCursor(cursor articleDomain.Cursor) string {
	data, _ := json.Marshal(cursorPayload{Version: 2, SortAt: cursor.SortAt.UTC(), ArticleID: cursor.ArticleID})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(value string) (articleDomain.Cursor, error) {
	if len(value) > 1024 {
		return articleDomain.Cursor{}, articleDomain.ErrInvalidCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return articleDomain.Cursor{}, articleDomain.ErrInvalidCursor
	}
	var payload cursorPayload
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload.Version != 2 || payload.ArticleID <= 0 || payload.SortAt.IsZero() {
		return articleDomain.Cursor{}, articleDomain.ErrInvalidCursor
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return articleDomain.Cursor{}, articleDomain.ErrInvalidCursor
	}
	return articleDomain.Cursor{SortAt: payload.SortAt.UTC(), ArticleID: payload.ArticleID}, nil
}

func optionalString(value string) *string {
	value = articleDomain.NormalizeText(value)
	if value == "" {
		return nil
	}
	return &value
}

func normalizedOptional(value *string, limit int) *string {
	if value == nil {
		return nil
	}
	return optionalString(articleDomain.TruncateRunes(*value, limit))
}

func optionalBytes(value *string, limit int) *string {
	if value == nil {
		return nil
	}
	truncated, _ := articleDomain.TruncateUTF8Bytes(*value, limit)
	return optionalString(truncated)
}

func truncatedOptional(value *string, limit int) (*string, bool) {
	if value == nil {
		return nil, false
	}
	truncated, wasTruncated := articleDomain.TruncateUTF8Bytes(*value, limit)
	return optionalString(truncated), wasTruncated
}

func utcPointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}
