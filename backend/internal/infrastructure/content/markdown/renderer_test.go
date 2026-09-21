package markdown

import (
	"errors"
	"strings"
	"testing"
)

const testAssetID = "123e4567-e89b-12d3-a456-426614174000"

func TestRenderNormalizesAndExtractsAssets(t *testing.T) {
	renderer := NewUserRenderer()
	result, err := renderer.Render("  标题  ", "正文\r\n\r\n![图](asset:"+testAssetID+")", Publish)
	if err != nil {
		t.Fatal(err)
	}
	if result.Title != "标题" || strings.Contains(result.NormalizedMarkdown, "\r") {
		t.Fatalf("规范化结果 = %q/%q", result.Title, result.NormalizedMarkdown)
	}
	if len(result.AssetIDs) != 1 || result.AssetIDs[0] != testAssetID {
		t.Fatalf("资产 = %v", result.AssetIDs)
	}
	if !strings.Contains(result.HTML, `/api/v1/assets/`+testAssetID+`/content`) || result.PlainText != "正文" {
		t.Fatalf("渲染结果 = %q / %q", result.HTML, result.PlainText)
	}
	if len(result.ContentHash) != 64 {
		t.Fatalf("内容哈希 = %q", result.ContentHash)
	}
}

func TestRenderDisablesRawHTMLAndRejectsExternalImages(t *testing.T) {
	renderer := NewUserRenderer()
	result, err := renderer.Render("标题", "<script>alert(1)</script><img src=\"https://evil.example/x\">\n\n正文", Publish)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.HTML, "script") || strings.Contains(result.HTML, "evil.example") {
		t.Fatalf("原始 HTML 生效: %q", result.HTML)
	}
	if _, err := renderer.Render("标题", `![外部](https://evil.example/x.png)`, Publish); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("外部图片应被拒绝: %v", err)
	}
}

func TestRenderDraftAndPublishValidation(t *testing.T) {
	renderer := NewUserRenderer()
	if _, err := renderer.Render("", "", Draft); err != nil {
		t.Fatalf("空草稿应允许: %v", err)
	}
	if _, err := renderer.Render("", "", Publish); !errors.Is(err, ErrEmptyPublished) {
		t.Fatalf("空发布应拒绝: %v", err)
	}
	if _, err := renderer.Render(strings.Repeat("界", MaxTitleRunes+1), "正文", Draft); !errors.Is(err, ErrTitleTooLong) {
		t.Fatalf("超长 Unicode 标题应拒绝: %v", err)
	}
	if _, err := renderer.Render("标题", strings.Repeat("a", MaxMarkdownBytes+1), Draft); !errors.Is(err, ErrMarkdownTooLarge) {
		t.Fatalf("超大 Markdown 应拒绝: %v", err)
	}
	invalidUTF8 := string([]byte{0xff})
	if _, err := renderer.Render("标题", invalidUTF8, Draft); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("非法 UTF-8 应拒绝: %v", err)
	}
}

func TestRenderImageCountAndDeduplicatedReferences(t *testing.T) {
	renderer := NewUserRenderer()
	twenty := strings.Repeat("![](asset:"+testAssetID+")\n", MaxImages)
	result, err := renderer.Render("标题", twenty, Publish)
	if err != nil || len(result.AssetIDs) != 1 {
		t.Fatalf("20 张重复图片边界应通过且引用去重: assets=%v err=%v", result.AssetIDs, err)
	}
	if _, err := renderer.Render("标题", twenty+"![](asset:"+testAssetID+")", Publish); !errors.Is(err, ErrTooManyImages) {
		t.Fatalf("第 21 张图片应拒绝: %v", err)
	}
}

func TestRenderHashUsesNormalizedContent(t *testing.T) {
	renderer := NewUserRenderer()
	a, err := renderer.Render("标题", "正文\r\n", Publish)
	if err != nil {
		t.Fatal(err)
	}
	b, err := renderer.Render(" 标题 ", "正文\n", Publish)
	if err != nil {
		t.Fatal(err)
	}
	if a.ContentHash != b.ContentHash {
		t.Fatalf("等价规范化输入哈希不同: %s/%s", a.ContentHash, b.ContentHash)
	}
}
