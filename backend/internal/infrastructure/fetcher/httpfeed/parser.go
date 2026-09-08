package httpfeed

import (
	"bytes"
	"context"
	"net/url"
	"strings"

	"github.com/mmcdole/gofeed"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

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
		// 正文中的相对 URL 以文章页为基准解析，否则图片/链接在清洗后会丢失
		contentBase := resolved
		if contentBase == "" {
			contentBase = baseURL
		}
		result.Items = append(result.Items, ports.ParsedItem{
			ID: optional(item.GUID), URL: resolved, Title: item.Title,
			AuthorName: authorName(item), Description: resolveContentURLs(optional(item.Description), contentBase), Content: resolveContentURLs(optional(item.Content), contentBase),
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

// resolveContentURLs 把 HTML 片段中 a/img 的相对 URL 解析为绝对地址，返回重排后的片段。
func resolveContentURLs(value *string, base string) *string {
	if value == nil {
		return nil
	}
	nodes, err := html.ParseFragment(strings.NewReader(*value), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return value
	}
	var output bytes.Buffer
	for _, node := range nodes {
		resolveNodeURLs(node, base)
		_ = html.Render(&output, node)
	}
	resolved := output.String()
	return &resolved
}

func resolveNodeURLs(node *html.Node, base string) {
	if node.Type == html.ElementNode {
		attribute := ""
		switch node.Data {
		case "a", "area":
			attribute = "href"
		case "img":
			attribute = "src"
		}
		if attribute != "" {
			for index, attr := range node.Attr {
				if attr.Key == attribute {
					if resolved := resolveURL(attr.Val, base); resolved != "" {
						node.Attr[index].Val = resolved
					}
				}
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		resolveNodeURLs(child, base)
	}
}
