package httpfeed

import (
	"bytes"
	"context"
	"net/url"
	"strings"

	"github.com/mmcdole/gofeed"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

type Parser struct{}

func NewParser() *Parser { return &Parser{} }

func (p *Parser) Parse(ctx context.Context, body []byte, baseURL string) (ports.ParsedFeed, error) {
	if err := ctx.Err(); err != nil {
		return ports.ParsedFeed{}, err
	}
	parser := gofeed.NewParser()
	parser.MaxByteSize = maxResponseBytes
	feed, err := parser.Parse(bytes.NewReader(body))
	if err != nil {
		return ports.ParsedFeed{}, &Error{code: "PARSE_FAILED", err: err}
	}
	result := ports.ParsedFeed{Title: feed.Title, SiteURL: safeResolvedURL(feed.Link, baseURL)}
	count := len(feed.Items)
	if count > 500 {
		count = 500
	}
	result.Items = make([]ports.ParsedItem, 0, count)
	for _, item := range feed.Items[:count] {
		if err := ctx.Err(); err != nil {
			return ports.ParsedFeed{}, err
		}
		if item == nil {
			continue
		}
		resolved := resolveURL(item.Link, baseURL)
		result.Items = append(result.Items, ports.ParsedItem{
			ID: optional(item.GUID), URL: resolved, Title: item.Title,
			AuthorName: authorName(item), Description: optional(item.Description), Content: optional(item.Content),
			Language: feed.Language, PublishedAt: item.PublishedParsed, UpdatedAt: item.UpdatedParsed,
		})
	}
	return result, nil
}

func authorName(item *gofeed.Item) *string {
	if item.Author != nil {
		return optional(item.Author.Name)
	}
	if len(item.Authors) > 0 && item.Authors[0] != nil {
		return optional(item.Authors[0].Name)
	}
	return nil
}

func safeResolvedURL(raw, base string) *string {
	value := resolveURL(raw, base)
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil
	}
	return &value
}

func resolveURL(raw, base string) string {
	rawURL, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	if rawURL.IsAbs() {
		return rawURL.String()
	}
	baseValue, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return baseValue.ResolveReference(rawURL).String()
}

func optional(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
