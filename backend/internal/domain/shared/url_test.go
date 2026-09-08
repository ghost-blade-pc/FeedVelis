package shared

import (
	"strings"
	"testing"
)

func TestNormalizeHTTPURLPreservesEncodedSlashAndQueryOrder(t *testing.T) {
	got, err := NormalizeHTTPURL("HTTPS://Example.COM:443/a/%2F/../b?z=1&a=2#part", 4096)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com/a/b?z=1&a=2" {
		t.Fatalf("got=%q", got)
	}
	got, err = NormalizeHTTPURL("https://example.com/a%2Fb", 4096)
	if err != nil || got != "https://example.com/a%2Fb" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestNormalizeHTTPURLChecksEncodedLength(t *testing.T) {
	if _, err := NormalizeHTTPURL("https://example.com/"+strings.Repeat("你", 10), 40); err == nil {
		t.Fatal("expected encoded URL length rejection")
	}
}

func TestNormalizeHTTPURLRemovesOnlyDotSegments(t *testing.T) {
	tests := map[string]string{
		"https://example.com/feed":         "https://example.com/feed",
		"https://example.com/feed/":        "https://example.com/feed/",
		"https://example.com/a//b":         "https://example.com/a//b",
		"https://example.com/a/./b/../c/":  "https://example.com/a/c/",
		"https://example.com/%2e/%2E%2E/x": "https://example.com/%2e/%2E%2E/x",
	}
	for raw, want := range tests {
		got, err := NormalizeHTTPURL(raw, 4096)
		if err != nil || got != want {
			t.Errorf("NormalizeHTTPURL(%q)=%q err=%v want=%q", raw, got, err, want)
		}
	}
}
