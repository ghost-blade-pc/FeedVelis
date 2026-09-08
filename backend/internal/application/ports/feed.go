package ports

import (
	"context"
	"time"
)

type FetchRequest struct {
	URL          string
	ETag         *string
	LastModified *string
}

type FetchResponse struct {
	NotModified  bool
	Body         []byte
	FinalURL     string
	ETag         *string
	LastModified *string
}

type FeedFetcher interface {
	Fetch(context.Context, FetchRequest) (FetchResponse, error)
}

type ParsedFeed struct {
	Title   string
	SiteURL *string
	Items   []ParsedItem
}

type ParsedItem struct {
	ID          *string
	URL         string
	Title       string
	AuthorName  *string
	Description *string
	Content     *string
	Language    string
	PublishedAt *time.Time
	UpdatedAt   *time.Time
}

type FeedParser interface {
	Parse(context.Context, []byte, string) (ParsedFeed, error)
}

type SanitizedContent struct {
	HTML      string
	PlainText string
}

type ContentSanitizer interface {
	Sanitize(string) SanitizedContent
}

type Clock interface {
	Now() time.Time
}
