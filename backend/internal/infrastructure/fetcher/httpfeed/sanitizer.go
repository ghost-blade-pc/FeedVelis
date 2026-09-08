package httpfeed

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

type Sanitizer struct{ policy *bluemonday.Policy }

func NewSanitizer() *Sanitizer {
	policy := bluemonday.NewPolicy()
	policy.AllowElements("p", "br", "h1", "h2", "h3", "h4", "h5", "h6", "strong", "em", "blockquote", "code", "pre", "ul", "ol", "li", "a")
	policy.AllowAttrs("href").Matching(regexp.MustCompile(`(?i)^https?://`)).OnElements("a")
	policy.AllowURLSchemes("http", "https")
	policy.RequireNoFollowOnLinks(true)
	policy.RequireNoReferrerOnLinks(true)
	policy.AddTargetBlankToFullyQualifiedLinks(true)
	return &Sanitizer{policy: policy}
}

func (s *Sanitizer) Sanitize(value string) ports.SanitizedContent {
	cleaned := s.policy.Sanitize(value)
	nodes, err := html.ParseFragment(strings.NewReader(cleaned), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return ports.SanitizedContent{PlainText: strings.TrimSpace(htmlToText(cleaned))}
	}
	var output bytes.Buffer
	for _, node := range nodes {
		hardenLinks(node)
		_ = html.Render(&output, node)
	}
	htmlValue := strings.TrimSpace(output.String())
	return ports.SanitizedContent{HTML: htmlValue, PlainText: strings.TrimSpace(htmlToText(htmlValue))}
}

func hardenLinks(node *html.Node) {
	if node.Type == html.ElementNode && node.Data == "a" {
		attrs := make([]html.Attribute, 0, len(node.Attr)+2)
		for _, attr := range node.Attr {
			if attr.Key != "rel" && attr.Key != "target" {
				attrs = append(attrs, attr)
			}
		}
		node.Attr = append(attrs,
			html.Attribute{Key: "rel", Val: "nofollow noopener noreferrer"},
			html.Attribute{Key: "target", Val: "_blank"},
		)
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		hardenLinks(child)
	}
}

func htmlToText(value string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(value))
	var text strings.Builder
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return strings.Join(strings.Fields(text.String()), " ")
		case html.TextToken:
			text.Write(tokenizer.Text())
			text.WriteByte(' ')
		}
	}
}
