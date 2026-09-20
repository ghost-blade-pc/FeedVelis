package account

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
)

// ---- 账户 ----

type fakeUsers struct {
	byID       map[accountDomain.UUID]accountDomain.User
	byUsername map[string]accountDomain.UUID
	auditOrder []string
}

func newFakeUsers() *fakeUsers {
	return &fakeUsers{byID: map[accountDomain.UUID]accountDomain.User{}, byUsername: map[string]accountDomain.UUID{}}
}

func (f *fakeUsers) Create(_ context.Context, user accountDomain.User) (accountDomain.User, error) {
	if _, ok := f.byUsername[user.Username]; ok {
		return accountDomain.User{}, accountDomain.ErrUsernameTaken
	}
	user.Version = 1
	f.byID[user.ID] = user
	f.byUsername[user.Username] = user.ID
	return user, nil
}

func (f *fakeUsers) GetByUsername(_ context.Context, username string) (accountDomain.User, error) {
	id, ok := f.byUsername[username]
	if !ok {
		return accountDomain.User{}, accountDomain.ErrNotFound
	}
	return f.byID[id], nil
}

func (f *fakeUsers) GetByID(_ context.Context, id accountDomain.UUID) (accountDomain.User, error) {
	user, ok := f.byID[id]
	if !ok {
		return accountDomain.User{}, accountDomain.ErrNotFound
	}
	return user, nil
}

func (f *fakeUsers) UpdateNickname(_ context.Context, id accountDomain.UUID, nickname string, expectedVersion int64, now time.Time) (accountDomain.User, error) {
	user, ok := f.byID[id]
	if !ok {
		return accountDomain.User{}, accountDomain.ErrNotFound
	}
	if user.Version != expectedVersion {
		return accountDomain.User{}, accountDomain.ErrVersionConflict
	}
	user.Nickname = nickname
	user.Version++
	user.UpdatedAt = now
	f.byID[id] = user
	return user, nil
}

func (f *fakeUsers) SetRole(_ context.Context, id accountDomain.UUID, role accountDomain.Role, now time.Time) (accountDomain.User, error) {
	return f.mutate(id, func(user *accountDomain.User) { user.Role = role }, now)
}

func (f *fakeUsers) SetStatus(_ context.Context, id accountDomain.UUID, status accountDomain.Status, now time.Time) (accountDomain.User, error) {
	return f.mutate(id, func(user *accountDomain.User) { user.Status = status }, now)
}

func (f *fakeUsers) mutate(id accountDomain.UUID, apply func(*accountDomain.User), now time.Time) (accountDomain.User, error) {
	user, ok := f.byID[id]
	if !ok {
		return accountDomain.User{}, accountDomain.ErrNotFound
	}
	apply(&user)
	user.Version++
	user.UpdatedAt = now
	f.byID[id] = user
	return user, nil
}

func (f *fakeUsers) CountActiveAdmins(context.Context) (int, error) {
	count := 0
	for _, user := range f.byID {
		if user.Role == accountDomain.RoleAdmin && user.Status == accountDomain.StatusActive {
			count++
		}
	}
	return count, nil
}

// ---- 会话与刷新令牌 ----

type fakeSessions struct {
	byID          map[accountDomain.UUID]accountDomain.Session
	revokedAll    int
	lastReason    accountDomain.RevokeReason
	sessionWrites int
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{byID: map[accountDomain.UUID]accountDomain.Session{}}
}

func (f *fakeSessions) Create(_ context.Context, session accountDomain.Session) error {
	session.Version = 1
	f.byID[session.ID] = session
	f.sessionWrites++
	return nil
}

func (f *fakeSessions) GetByID(_ context.Context, id accountDomain.UUID) (accountDomain.Session, error) {
	session, ok := f.byID[id]
	if !ok {
		return accountDomain.Session{}, accountDomain.ErrSessionInvalid
	}
	return session, nil
}

func (f *fakeSessions) Revoke(_ context.Context, id accountDomain.UUID, reason accountDomain.RevokeReason, now time.Time) error {
	session, ok := f.byID[id]
	if !ok || session.RevokedAt != nil {
		return nil
	}
	session.RevokedAt = &now
	session.RevokeReason = &reason
	f.byID[id] = session
	return nil
}

func (f *fakeSessions) RevokeAllForUser(_ context.Context, userID accountDomain.UUID, reason accountDomain.RevokeReason, now time.Time) (int64, error) {
	var affected int64
	for id, session := range f.byID {
		if session.UserID != userID || session.RevokedAt != nil {
			continue
		}
		session.RevokedAt = &now
		session.RevokeReason = &reason
		f.byID[id] = session
		affected++
	}
	f.revokedAll++
	f.lastReason = reason
	return affected, nil
}

type fakeTokens struct {
	sessions  *fakeSessions
	byDigest  map[[32]byte]accountDomain.RefreshToken
	rotations int
}

func newFakeTokens(sessions *fakeSessions) *fakeTokens {
	return &fakeTokens{sessions: sessions, byDigest: map[[32]byte]accountDomain.RefreshToken{}}
}

func (f *fakeTokens) Create(_ context.Context, token accountDomain.RefreshToken) error {
	f.byDigest[token.Digest] = token
	return nil
}

func (f *fakeTokens) FindByDigest(_ context.Context, digest [32]byte) (accountDomain.RefreshToken, error) {
	token, ok := f.byDigest[digest]
	if !ok {
		return accountDomain.RefreshToken{}, accountDomain.ErrRefreshUnknown
	}
	return token, nil
}

// Rotate 复刻仓储语义：未知摘要返回 ErrRefreshUnknown，已消费摘要撤销会话并返回 ErrRefreshReplay。
func (f *fakeTokens) Rotate(_ context.Context, digest [32]byte, next accountDomain.RefreshToken, now time.Time) (accountDomain.Session, error) {
	stored, ok := f.byDigest[digest]
	if !ok {
		return accountDomain.Session{}, accountDomain.ErrRefreshUnknown
	}
	session, ok := f.sessions.byID[stored.SessionID]
	if !ok {
		return accountDomain.Session{}, accountDomain.ErrSessionInvalid
	}
	if stored.ConsumedAt != nil {
		_ = f.sessions.Revoke(context.Background(), session.ID, accountDomain.RevokeReasonReplay, now)
		return accountDomain.Session{}, accountDomain.ErrRefreshReplay
	}
	if !session.IsActive(now) {
		return accountDomain.Session{}, accountDomain.ErrSessionInvalid
	}
	stored.ConsumedAt = &now
	f.byDigest[digest] = stored
	next.SessionID = session.ID
	next.ExpiresAt = session.ExpiresAt
	f.byDigest[next.Digest] = next
	session.LastRefreshedAt = now
	f.sessions.byID[session.ID] = session
	f.rotations++
	return session, nil
}

// ---- 限流 ----

type fakeThrottle struct {
	failures map[string][]time.Time
	blocks   map[string]time.Time
}

func newFakeThrottle() *fakeThrottle {
	return &fakeThrottle{failures: map[string][]time.Time{}, blocks: map[string]time.Time{}}
}

func throttleKey(dimension accountDomain.FailureDimension, key []byte) string {
	return string(dimension) + ":" + hex.EncodeToString(key)
}

func (f *fakeThrottle) ActiveBlock(_ context.Context, dimension accountDomain.FailureDimension, key []byte, now time.Time) (*time.Time, error) {
	until, ok := f.blocks[throttleKey(dimension, key)]
	if !ok || !until.After(now) {
		return nil, nil
	}
	return &until, nil
}

func (f *fakeThrottle) RecordFailure(_ context.Context, dimension accountDomain.FailureDimension, key []byte, at time.Time, policy accountDomain.ThrottlePolicy) (*time.Time, error) {
	lookup := throttleKey(dimension, key)
	if until, ok := f.blocks[lookup]; ok && until.After(at) {
		return &until, nil
	}
	window := at.Add(-policy.Window(dimension))
	kept := make([]time.Time, 0, len(f.failures[lookup])+1)
	for _, occurred := range f.failures[lookup] {
		if occurred.After(window) {
			kept = append(kept, occurred)
		}
	}
	kept = append(kept, at)
	f.failures[lookup] = kept
	if len(kept) < policy.Limit(dimension) {
		return nil, nil
	}
	until := policy.BlockedUntil(at)
	f.blocks[lookup] = until
	return &until, nil
}

// ---- 其余端口 ----

type fakeAudits struct{ entries []accountDomain.AuditLog }

func (f *fakeAudits) Record(_ context.Context, entry accountDomain.AuditLog) error {
	f.entries = append(f.entries, entry)
	return nil
}

type fakeLocks struct {
	users *fakeUsers
	calls []string
}

func (f *fakeLocks) LockAdminScope(context.Context) error {
	f.calls = append(f.calls, "admin_scope")
	return nil
}

func (f *fakeLocks) LockUser(ctx context.Context, id accountDomain.UUID) (accountDomain.User, error) {
	f.calls = append(f.calls, "user:"+id.String())
	return f.users.GetByID(ctx, id)
}

type fakeHasher struct {
	encoded  string
	matches  bool
	err      error
	hashes   int
	lastHash string
}

func (f *fakeHasher) Hash(password string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.hashes++
	f.lastHash = password
	if f.encoded != "" {
		return f.encoded, nil
	}
	return "$argon2id$v=19$m=19456,t=2,p=1$ZmFrZXNhbHQ$ZmFrZWhhc2g", nil
}

func (f *fakeHasher) Verify(string, string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.matches, nil
}

// fakeSigner 用可解析的字符串代替真实签名，便于身份解析测试。
type fakeSigner struct{ signed int }

func (f *fakeSigner) Sign(claims ports.AccessClaims) (string, error) {
	f.signed++
	return strings.Join([]string{"access", claims.Subject, claims.SessionID, claims.TokenID}, "|"), nil
}

func (f *fakeSigner) Verify(token string) (ports.AccessClaims, error) {
	parts := strings.Split(token, "|")
	if len(parts) != 4 || parts[0] != "access" {
		return ports.AccessClaims{}, fmt.Errorf("令牌无效")
	}
	return ports.AccessClaims{Subject: parts[1], SessionID: parts[2], TokenID: parts[3]}, nil
}

type fakeSecrets struct{ counter int }

func (f *fakeSecrets) NewRefreshToken() (string, [32]byte, error) {
	f.counter++
	raw := fmt.Sprintf("refresh-%d", f.counter)
	return raw, sha256.Sum256([]byte(raw)), nil
}

func (f *fakeSecrets) Digest(raw string) [32]byte { return sha256.Sum256([]byte(raw)) }

type fakeThrottleKeys struct{}

func (fakeThrottleKeys) AccountKey(username string) []byte { return []byte("account:" + username) }

func (fakeThrottleKeys) IPKey(ip string) []byte { return []byte("ip:" + ip) }

type fakeBlocklist map[string]struct{}

func (f fakeBlocklist) Contains(loweredPassword string) bool {
	_, ok := f[loweredPassword]
	return ok
}

type fakeTx struct{ calls int }

func (f *fakeTx) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	f.calls++
	return fn(ctx)
}

type fakeClock struct{ now time.Time }

func (f *fakeClock) Now() time.Time { return f.now }
