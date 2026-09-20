package account

import (
	"errors"
	"testing"
	"time"
)

func TestNewSessionUsesAbsoluteTTL(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	session := NewSession(UUID{1}, UUID{2}, now, 0)
	if got := session.ExpiresAt.Sub(now); got != SessionTTL {
		t.Fatalf("会话期限 = %v", got)
	}
	if !session.IsActive(now.Add(SessionTTL - time.Second)) {
		t.Fatal("期限内应有效")
	}
	if session.IsActive(now.Add(SessionTTL)) {
		t.Fatal("到期后应失效")
	}
	if got := session.RemainingTTL(now.Add(SessionTTL)); got != 0 {
		t.Fatalf("到期剩余 = %v", got)
	}
}

func TestAccessExpiryCappedBySession(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	session := NewSession(UUID{1}, UUID{2}, now, SessionTTL)
	expiry, err := session.AccessExpiry(now, AccessTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	if got := expiry.Sub(now); got != AccessTokenTTL {
		t.Fatalf("访问令牌期限 = %v", got)
	}
	late := session.ExpiresAt.Add(-time.Minute)
	expiry, err = session.AccessExpiry(late, AccessTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	if got := expiry.Sub(late); got != time.Minute {
		t.Fatalf("访问令牌不得超过会话剩余期限，实际 %v", got)
	}
	custom, err := NewSession(UUID{1}, UUID{2}, now, 0).AccessExpiry(now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got := custom.Sub(now); got != 10*time.Minute {
		t.Fatalf("配置的访问令牌期限应生效，实际 %v", got)
	}
	if _, err := session.AccessExpiry(session.ExpiresAt, AccessTokenTTL); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("过期会话应返回无效: %v", err)
	}
}

func TestRevokedSessionIsInactive(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	session := NewSession(UUID{1}, UUID{2}, now, SessionTTL)
	revokedAt := now.Add(time.Minute)
	reason := RevokeReasonLogout
	session.RevokedAt = &revokedAt
	session.RevokeReason = &reason
	if session.IsActive(now.Add(2 * time.Minute)) {
		t.Fatal("已撤销会话不得有效")
	}
}

func TestRefreshTokenRules(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	session := NewSession(UUID{1}, UUID{2}, now, SessionTTL)
	token, err := session.NewRefreshToken(UUID{3}, [32]byte{9}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !token.ExpiresAt.Equal(session.ExpiresAt) {
		t.Fatalf("刷新令牌有效期应等于会话剩余期限: %v", token.ExpiresAt)
	}
	if !token.IsUsable(now) {
		t.Fatal("新令牌应可用")
	}
	consumedAt := now.Add(time.Minute)
	token.ConsumedAt = &consumedAt
	if token.IsUsable(now.Add(2 * time.Minute)) {
		t.Fatal("已消费令牌不得再次使用")
	}
}
