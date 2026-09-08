package article

import (
	"strings"
	"testing"
)

func TestDedupeKeyPrefersID(t *testing.T) {
	id := "ABC"
	a, err := DedupeKey(&id, "https://example.com/a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := DedupeKey(&id, "https://example.com/b")
	if err != nil {
		t.Fatal(err)
	}
	if a != b || len(a) != 64 {
		t.Fatalf("keys = %q %q", a, b)
	}
}

func TestTruncateUTF8Bytes(t *testing.T) {
	value, truncated := TruncateUTF8Bytes(strings.Repeat("你", 5), 10)
	if !truncated || value != "你你你" {
		t.Fatalf("value=%q truncated=%t", value, truncated)
	}
}

func TestNormalizeCanonicalURLRejectsDangerousSchemes(t *testing.T) {
	for _, raw := range []string{"javascript:alert(1)", "data:text/plain,x", "ftp://example.com/a"} {
		if _, err := NormalizeCanonicalURL(raw); err == nil {
			t.Fatalf("expected %q invalid", raw)
		}
	}
}

func TestContentHashIncludesTruncationFlags(t *testing.T) {
	rawDescription := strings.Repeat("d", MaxRawDescriptionBytes)
	rawContent := strings.Repeat("c", MaxRawContentBytes)
	article := Article{CanonicalURL: "https://example.com/article", Title: "标题", Language: "zh-CN"}
	base := Content{RawDescription: &rawDescription, RawContent: &rawContent}

	descriptionTruncated := base
	descriptionTruncated.RawDescriptionTruncated = true
	if ContentHash(article, base) == ContentHash(article, descriptionTruncated) {
		t.Fatal("摘要截断标志变化必须改变 content hash")
	}

	contentTruncated := base
	contentTruncated.RawContentTruncated = true
	if ContentHash(article, base) == ContentHash(article, contentTruncated) {
		t.Fatal("正文截断标志变化必须改变 content hash")
	}
}

func TestContentHashIncludesSanitizerVersion(t *testing.T) {
	raw := "d"
	article := Article{CanonicalURL: "https://example.com/article", Title: "标题"}
	base := Content{RawDescription: &raw, SanitizerVersion: 1}
	upgraded := base
	upgraded.SanitizerVersion = 2
	if ContentHash(article, base) == ContentHash(article, upgraded) {
		t.Fatal("清洗器版本升级必须改变 content hash，以触发旧数据重洗")
	}
}
