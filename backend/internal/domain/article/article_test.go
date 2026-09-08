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
