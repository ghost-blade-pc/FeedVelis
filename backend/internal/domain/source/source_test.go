package source

import (
	"testing"
	"time"
)

func TestNormalizeFeedURL(t *testing.T) {
	got, err := NormalizeFeedURL("HTTPS://例子.测试:443/a/../feed.xml#part")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://xn--fsqu00a.xn--0zwm56d/feed.xml" {
		t.Fatalf("normalized = %q", got)
	}
	for _, raw := range []string{"file:///tmp/a", "https://user@example.com/feed", "http://example.com:8080/feed"} {
		if _, err := NormalizeFeedURL(raw); err == nil {
			t.Fatalf("expected %q invalid", raw)
		}
	}
}

func TestNextFailure(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	update := NextFailure(now, 4, "FETCH_TIMEOUT", 0)
	if update.Status != StatusDegraded || update.ConsecutiveFailures != 5 {
		t.Fatalf("unexpected update: %+v", update)
	}
}

func TestNextFailureBackoffAndJitterBounds(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	first := NextFailure(now, 0, "FETCH_FAILED", -1)
	if got := first.NextFetchAt.Sub(now); got != 48*time.Second {
		t.Fatalf("first delay=%v", got)
	}
	capped := NextFailure(now, 20, "FETCH_FAILED", 1)
	if got := capped.NextFetchAt.Sub(now); got != 6*time.Hour || capped.Status != StatusDegraded {
		t.Fatalf("capped=%+v", capped)
	}
}
