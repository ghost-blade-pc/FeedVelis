package account

import (
	"errors"
	"time"
)

const (
	SessionTTL     = 168 * time.Hour
	AccessTokenTTL = 15 * time.Minute
)

var (
	ErrSessionInvalid = errors.New("会话无效")
	ErrRefreshUnknown = errors.New("刷新令牌无效")
	ErrRefreshReplay  = errors.New("刷新令牌已被使用")
)

type RevokeReason string

const (
	RevokeReasonLogout      RevokeReason = "logout"
	RevokeReasonDisabled    RevokeReason = "disabled"
	RevokeReasonRoleChanged RevokeReason = "role_changed"
	RevokeReasonReplay      RevokeReason = "replay"
)

// Session 是一次登录建立的独立会话，ExpiresAt 是登录时确定的绝对期限。
type Session struct {
	ID              UUID
	UserID          UUID
	CreatedAt       time.Time
	ExpiresAt       time.Time
	LastRefreshedAt time.Time
	RevokedAt       *time.Time
	RevokeReason    *RevokeReason
	Version         int64
}

func (s Session) IsActive(now time.Time) bool {
	return s.RevokedAt == nil && now.Before(s.ExpiresAt)
}

// RemainingTTL 返回会话剩余绝对期限，已撤销或已过期时为 0。
func (s Session) RemainingTTL(now time.Time) time.Duration {
	if !s.IsActive(now) {
		return 0
	}
	return s.ExpiresAt.Sub(now)
}

// AccessExpiry 返回访问令牌到期时间，不超过访问令牌期限且不超过会话剩余期限。
func (s Session) AccessExpiry(now time.Time, accessTTL time.Duration) (time.Time, error) {
	remaining := s.RemainingTTL(now)
	if remaining <= 0 {
		return time.Time{}, ErrSessionInvalid
	}
	if accessTTL <= 0 || accessTTL > remaining {
		accessTTL = remaining
	}
	return now.Add(accessTTL), nil
}

// RefreshToken 只保存摘要；轮换在同一事务内消费旧令牌并签发后继令牌。
type RefreshToken struct {
	ID          UUID
	SessionID   UUID
	Digest      [32]byte
	IssuedAt    time.Time
	ExpiresAt   time.Time
	ConsumedAt  *time.Time
	SuccessorID *UUID
}

func (t RefreshToken) IsUsable(now time.Time) bool {
	return t.ConsumedAt == nil && now.Before(t.ExpiresAt)
}

// NewSession 建立登录时的会话；ttl 为配置或默认的绝对期限，非正数时退回 SessionTTL。
func NewSession(id, userID UUID, now time.Time, ttl time.Duration) Session {
	if ttl <= 0 {
		ttl = SessionTTL
	}
	return Session{
		ID:              id,
		UserID:          userID,
		CreatedAt:       now,
		ExpiresAt:       now.Add(ttl),
		LastRefreshedAt: now,
	}
}

// NewRefreshToken 签发刷新令牌，有效期等于会话剩余绝对期限。
func (s Session) NewRefreshToken(id UUID, digest [32]byte, now time.Time) (RefreshToken, error) {
	remaining := s.RemainingTTL(now)
	if remaining <= 0 {
		return RefreshToken{}, ErrSessionInvalid
	}
	return RefreshToken{
		ID:        id,
		SessionID: s.ID,
		Digest:    digest,
		IssuedAt:  now,
		ExpiresAt: s.ExpiresAt,
	}, nil
}
