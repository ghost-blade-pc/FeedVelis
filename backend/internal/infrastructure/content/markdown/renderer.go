package markdown

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"golang.org/x/net/html"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
)

const (
	MaxTitleRunes    = 200
	MaxMarkdownBytes = 256 * 1024
	MaxImages        = 20
	MaxExcerptRunes  = 300
	SanitizerVersion = 1
)

var (
	ErrInvalidUTF8      = errors.New("Markdown 必须是有效 UTF-8")
	ErrTitleTooLong     = errors.New("标题不能超过 200 个 Unicode 字符")
	ErrMarkdownTooLarge = errors.New("Markdown 不能超过 256 KiB")
	ErrInvalidImage     = errors.New("图片必须使用 asset:<uuid> 引用")
	ErrTooManyImages    = errors.New("每篇文章最多引用 20 张图片")
	ErrEmptyPublished   = errors.New("发布文章必须包含标题以及可见文本或有效图片")
)

type Mode int

const (
	Draft Mode = iota
	Publish
)

type Result struct {
	Title              string
	NormalizedMarkdown string
	HTML               string
	PlainText          string
	Excerpt            string
	ContentHash        string
	AssetIDs           []string
}

func (r *UserRenderer) RenderUserContent(title, markdown string, mode ports.UserContentMode) (ports.RenderedUserContent, error) {
	renderMode := Draft
	if mode == ports.UserContentPublish {
		renderMode = Publish
	}
	result, err := r.Render(title, markdown, renderMode)
	if err != nil {
		// 标注为内容校验失败，接口层据此返回 400 ARTICLE_CONTENT_INVALID。
		return ports.RenderedUserContent{}, ports.InvalidContent(err)
	}
	return ports.RenderedUserContent{
		Title: result.Title, Markdown: result.NormalizedMarkdown, HTML: result.HTML,
		PlainText: result.PlainText, Excerpt: result.Excerpt, Hash: result.ContentHash,
		AssetIDs: result.AssetIDs, Sanitizer: SanitizerVersion,
	}, nil
}

// UserRenderer 是用户稿件受控渲染器。
type UserRenderer struct {
	policy *bluemonday.Policy
}

// NewUserRenderer 创建只允许站内资产图片的渲染器。
func NewUserRenderer() *UserRenderer {
	policy := bluemonday.NewPolicy()
	policy.AllowElements("p", "br", "h1", "h2", "h3", "h4", "h5", "h6", "strong", "em", "blockquote", "code", "pre", "ul", "ol", "li", "a", "hr", "img")
	policy.AllowAttrs("href").Matching(regexp.MustCompile(`(?i)^https?://`)).OnElements("a")
	policy.AllowAttrs("src").Matching(regexp.MustCompile(`^/api/v1/assets/[0-9a-f-]{36}/content$`)).OnElements("img")
	policy.AllowAttrs("alt", "title").OnElements("img")
	policy.AllowRelativeURLs(true)
	policy.AllowURLSchemes("http", "https")
	policy.RequireNoFollowOnLinks(true)
	policy.RequireNoReferrerOnLinks(true)
	policy.AddTargetBlankToFullyQualifiedLinks(true)
	return &UserRenderer{policy: policy}
}

func (r *UserRenderer) Render(title, markdown string, mode Mode) (Result, error) {
	normalizedTitle, normalizedMarkdown, err := normalize(title, markdown)
	if err != nil {
		return Result{}, err
	}
	engine := NewEngine()
	source := []byte(normalizedMarkdown)
	document := engine.Parser().Parse(text.NewReader(source))
	assetIDs := make([]string, 0)
	seen := make(map[string]struct{})
	imageCount := 0
	err = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		image, ok := node.(*ast.Image)
		if !ok {
			return ast.WalkContinue, nil
		}
		imageCount++
		if imageCount > MaxImages {
			return ast.WalkStop, ErrTooManyImages
		}
		raw := string(image.Destination)
		if !strings.HasPrefix(raw, "asset:") {
			return ast.WalkStop, ErrInvalidImage
		}
		id, parseErr := uuid.Parse(strings.TrimPrefix(raw, "asset:"))
		if parseErr != nil || id == uuid.Nil {
			return ast.WalkStop, ErrInvalidImage
		}
		canonical := id.String()
		image.Destination = []byte("/api/v1/assets/" + canonical + "/content")
		if _, exists := seen[canonical]; !exists {
			seen[canonical] = struct{}{}
			assetIDs = append(assetIDs, canonical)
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return Result{}, err
	}

	var rendered bytes.Buffer
	if err := engine.Renderer().Render(&rendered, source, document); err != nil {
		return Result{}, fmt.Errorf("渲染 Markdown: %w", err)
	}
	cleanHTML := strings.TrimSpace(r.policy.Sanitize(rendered.String()))
	plainText := htmlText(cleanHTML)
	if mode == Publish && (normalizedTitle == "" || (plainText == "" && len(assetIDs) == 0)) {
		return Result{}, ErrEmptyPublished
	}
	excerpt := truncateRunes(plainText, MaxExcerptRunes)
	hashInput := fmt.Sprintf("v%d\x00%s\x00%s", SanitizerVersion, normalizedTitle, normalizedMarkdown)
	sum := sha256.Sum256([]byte(hashInput))
	return Result{
		Title: normalizedTitle, NormalizedMarkdown: normalizedMarkdown, HTML: cleanHTML,
		PlainText: plainText, Excerpt: excerpt, ContentHash: hex.EncodeToString(sum[:]), AssetIDs: assetIDs,
	}, nil
}

func normalize(title, markdown string) (string, string, error) {
	if !utf8.ValidString(title) || !utf8.ValidString(markdown) {
		return "", "", ErrInvalidUTF8
	}
	title = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(title, "\r\n", "\n"), "\r", "\n"))
	markdown = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(markdown, "\r\n", "\n"), "\r", "\n"))
	if utf8.RuneCountInString(title) > MaxTitleRunes {
		return "", "", ErrTitleTooLong
	}
	if len(markdown) > MaxMarkdownBytes {
		return "", "", ErrMarkdownTooLarge
	}
	return title, markdown, nil
}

func htmlText(value string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(value))
	var output strings.Builder
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return strings.Join(strings.Fields(output.String()), " ")
		case html.TextToken:
			output.Write(tokenizer.Text())
			output.WriteByte(' ')
		}
	}
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}
