package source

import (
	"errors"
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

func TestValidateFetchIntervalBoundaries(t *testing.T) {
	valid := []time.Duration{MinFetchInterval, 30 * time.Minute, 90 * time.Minute, MaxFetchInterval}
	for _, interval := range valid {
		if err := ValidateFetchInterval(interval); err != nil {
			t.Fatalf("%v 应合法: %v", interval, err)
		}
	}
	// 上下界之外一律拒绝：低于 5 分钟会压垮上游，高于 24 小时失去抓取意义。
	invalid := []time.Duration{0, -time.Minute, MinFetchInterval - time.Second, MaxFetchInterval + time.Second, 48 * time.Hour}
	for _, interval := range invalid {
		if err := ValidateFetchInterval(interval); !errors.Is(err, ErrInvalidInterval) {
			t.Fatalf("%v 应被拒绝，实际 err=%v", interval, err)
		}
	}
	// 亚秒精度会被数据库的秒级列悄悄截断，因此直接拒绝。
	if err := ValidateFetchInterval(5*time.Minute + time.Millisecond); !errors.Is(err, ErrInvalidInterval) {
		t.Fatalf("亚秒周期应被拒绝，实际 err=%v", err)
	}
}

func TestSourceFetchIntervalFallsBackToDefault(t *testing.T) {
	if got := (Source{}).FetchIntervalOr(); got != DefaultFetchInterval {
		t.Fatalf("零值周期 = %v", got)
	}
	if got := (Source{FetchInterval: 5 * time.Minute}).FetchIntervalOr(); got != 5*time.Minute {
		t.Fatalf("已设置周期 = %v", got)
	}
}

func TestCheckVersionRejectsStaleAndMissing(t *testing.T) {
	if err := CheckVersion(3, 3); err != nil {
		t.Fatalf("相同版本 err = %v", err)
	}
	for _, expected := range []int64{0, -1, 2, 4} {
		if err := CheckVersion(3, expected); !errors.Is(err, ErrVersionConflict) {
			t.Fatalf("期望 %d 应冲突，实际 err=%v", expected, err)
		}
	}
}
