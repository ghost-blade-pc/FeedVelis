package account

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
)

type harness struct {
	svc      *Service
	admin    *AdminService
	users    *fakeUsers
	sessions *fakeSessions
	tokens   *fakeTokens
	throttle *fakeThrottle
	audits   *fakeAudits
	locks    *fakeLocks
	hasher   *fakeHasher
	signer   *fakeSigner
	secrets  *fakeSecrets
	tx       *fakeTx
}

func newHarness(t *testing.T, mutate func(*Deps)) *harness {
	t.Helper()
	sessions := newFakeSessions()
	hasher := &fakeHasher{matches: true}
	users := newFakeUsers()
	h := &harness{
		users:    users,
		sessions: sessions,
		tokens:   newFakeTokens(sessions),
		throttle: newFakeThrottle(),
		audits:   &fakeAudits{},
		locks:    &fakeLocks{users: users},
		hasher:   hasher,
		signer:   &fakeSigner{},
		secrets:  &fakeSecrets{},
		tx:       &fakeTx{},
	}
	clock := &fakeClock{now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	deps := Deps{
		Users: h.users, Sessions: h.sessions, Tokens: h.tokens, Throttle: h.throttle,
		Hasher: hasher, Signer: h.signer,
		Blocklist: fakeBlocklist{}, ThrottleKeys: fakeThrottleKeys{}, Secrets: h.secrets,
		Tx: h.tx, Clock: clock,
		Policy: accountDomain.DefaultThrottlePolicy(), AccessTTL: 15 * time.Minute,
		SessionTTL: 168 * time.Hour, RegistrationEnabled: true,
	}
	if mutate != nil {
		mutate(&deps)
	}
	service, err := NewService(deps)
	if err != nil {
		t.Fatalf("构造用例: %v", err)
	}
	h.svc = service
	h.admin, err = NewAdminService(AdminDeps{
		Users: h.users, Sessions: h.sessions, Audits: h.audits, Locks: h.locks,
		Hasher: hasher, Blocklist: fakeBlocklist{}, Tx: h.tx, Clock: clock,
	})
	if err != nil {
		t.Fatalf("构造管理用例: %v", err)
	}
	return h
}

func (h *harness) seedUser(t *testing.T, username string, role accountDomain.Role, status accountDomain.Status) accountDomain.User {
	t.Helper()
	id, err := accountDomain.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	user, err := h.users.Create(context.Background(), accountDomain.User{
		ID: id, Username: username, Nickname: username, PasswordHash: "phc",
		Role: role, Status: status, CreatedAt: h.svc.now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func (h *harness) login(t *testing.T, username string) LoginResult {
	t.Helper()
	result, err := h.svc.Login(context.Background(), LoginInput{Username: username, Password: "Abcd123!", ClientIP: "203.0.113.7"})
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	return result
}

func TestNewServiceRejectsMissingDeps(t *testing.T) {
	if _, err := NewService(Deps{}); err == nil {
		t.Fatal("缺少依赖应被拒绝")
	}
}

func TestRegisterAppliesRulesAndDefaults(t *testing.T) {
	disabled := newHarness(t, func(deps *Deps) { deps.RegistrationEnabled = false })
	if _, err := disabled.svc.Register(context.Background(), RegisterInput{Username: "alice", Password: "Abcd123!"}); !errors.Is(err, ErrRegistrationDisabled) {
		t.Fatalf("注册关闭时=%v", err)
	}

	h := newHarness(t, nil)
	empty := ""
	if _, err := h.svc.Register(context.Background(), RegisterInput{Username: "alice", Password: "Abcd123!", Nickname: &empty}); !errors.Is(err, accountDomain.ErrInvalidNickname) {
		t.Fatalf("显式空昵称=%v", err)
	}
	if _, err := h.svc.Register(context.Background(), RegisterInput{Username: "alice", Password: "abcdefgh"}); !errors.Is(err, accountDomain.ErrPasswordClasses) {
		t.Fatalf("弱密码=%v", err)
	}

	user, err := h.svc.Register(context.Background(), RegisterInput{Username: "Alice", Password: "Abcd123!"})
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "alice" || user.Nickname != "alice" {
		t.Fatalf("规范化与默认昵称=%+v", user)
	}
	if user.Role != accountDomain.RoleUser || user.Status != accountDomain.StatusActive {
		t.Fatalf("新账户角色/状态=%s/%s", user.Role, user.Status)
	}
	if strings.Contains(user.PasswordHash, "Abcd123!") || h.hasher.lastHash != "Abcd123!" {
		t.Fatal("散列必须使用原始密码且不得保存明文")
	}
	if _, err := h.svc.Register(context.Background(), RegisterInput{Username: "alice", Password: "Abcd123!"}); !errors.Is(err, accountDomain.ErrUsernameTaken) {
		t.Fatalf("重名=%v", err)
	}
	if h.sessions.sessionWrites != 0 {
		t.Fatal("注册不得建立会话")
	}
}

func TestLoginOutcomesShareSingleError(t *testing.T) {
	h := newHarness(t, nil)
	h.seedUser(t, "alice", accountDomain.RoleUser, accountDomain.StatusActive)
	h.seedUser(t, "blocked_user", accountDomain.RoleUser, accountDomain.StatusDisabled)

	result := h.login(t, "alice")
	if result.AccessToken == "" || result.RefreshToken == "" || result.Session.ID.IsZero() {
		t.Fatalf("登录结果缺少令牌: %+v", result)
	}
	if got := result.RefreshExpiresAt.Sub(result.Session.CreatedAt); got != 168*time.Hour {
		t.Fatalf("刷新令牌期限=%v", got)
	}

	h.hasher.matches = false
	if _, err := h.svc.Login(context.Background(), LoginInput{Username: "alice", Password: "Abcd123!", ClientIP: "203.0.113.7"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("密码错误=%v", err)
	}
	h.hasher.matches = true
	if _, err := h.svc.Login(context.Background(), LoginInput{Username: "missing", Password: "Abcd123!", ClientIP: "203.0.113.7"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("未知账户=%v", err)
	}
	if _, err := h.svc.Login(context.Background(), LoginInput{Username: "blocked_user", Password: "Abcd123!", ClientIP: "203.0.113.7"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("禁用账户=%v", err)
	}
	if h.sessions.sessionWrites != 1 {
		t.Fatalf("仅成功登录可建会话，实际 %d", h.sessions.sessionWrites)
	}
	var accountKeys, ipKeys int
	for key := range h.throttle.failures {
		switch {
		case strings.HasPrefix(key, string(accountDomain.DimensionAccount)+":"):
			accountKeys++
		case strings.HasPrefix(key, string(accountDomain.DimensionIP)+":"):
			ipKeys++
		}
	}
	if accountKeys != 3 || ipKeys != 1 {
		t.Fatalf("失败计数维度分布: 账号 %d 个键、IP %d 个键", accountKeys, ipKeys)
	}
}

func TestLoginThrottleBlocksEvenCorrectPassword(t *testing.T) {
	h := newHarness(t, nil)
	user := h.seedUser(t, "alice", accountDomain.RoleUser, accountDomain.StatusActive)
	h.hasher.matches = false
	for i := range 5 {
		if _, err := h.svc.Login(context.Background(), LoginInput{Username: "alice", Password: "wrong", ClientIP: "203.0.113.7"}); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("第 %d 次失败=%v", i+1, err)
		}
	}
	h.hasher.matches = true
	_, err := h.svc.Login(context.Background(), LoginInput{Username: user.Username, Password: "Abcd123!", ClientIP: "203.0.113.7"})
	var limited *RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("限制期内必须拒绝: %v", err)
	}
	if limited.RetryAfter <= 0 || limited.RetryAfter > 15*time.Minute {
		t.Fatalf("剩余等待时间=%v", limited.RetryAfter)
	}
	if limited.Dimension != accountDomain.DimensionAccount {
		t.Fatalf("触发维度应为账号，实际 %q", limited.Dimension)
	}
	if h.sessions.sessionWrites != 0 {
		t.Fatal("限制期内不得建立会话")
	}
}

// IP 维度先于账号维度触发时，限流错误应标记为 IP 维度，便于按维度计数。
func TestLoginThrottleReportsIPDimension(t *testing.T) {
	h := newHarness(t, nil)
	h.seedUser(t, "alice", accountDomain.RoleUser, accountDomain.StatusActive)
	h.hasher.matches = false
	for i := range 30 {
		if _, err := h.svc.Login(context.Background(), LoginInput{
			Username: "ghost" + strconv.Itoa(i), Password: "wrong", ClientIP: "203.0.113.9",
		}); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("第 %d 次失败=%v", i+1, err)
		}
	}
	_, err := h.svc.Login(context.Background(), LoginInput{Username: "alice", Password: "Abcd123!", ClientIP: "203.0.113.9"})
	var limited *RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("IP 维度触发后必须拒绝: %v", err)
	}
	if limited.Dimension != accountDomain.DimensionIP {
		t.Fatalf("触发维度应为 IP，实际 %q", limited.Dimension)
	}
}

func TestRefreshRotationReplayAndUnknown(t *testing.T) {
	h := newHarness(t, nil)
	h.seedUser(t, "alice", accountDomain.RoleUser, accountDomain.StatusActive)
	login := h.login(t, "alice")

	refreshed, err := h.svc.Refresh(context.Background(), login.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.RefreshToken == login.RefreshToken {
		t.Fatal("刷新必须轮换令牌")
	}
	if refreshed.Session.ID != login.Session.ID {
		t.Fatal("刷新应保持同一会话")
	}
	if _, err := h.svc.Refresh(context.Background(), login.RefreshToken); !errors.Is(err, accountDomain.ErrRefreshReplay) {
		t.Fatalf("重放=%v", err)
	}
	session, err := h.sessions.GetByID(context.Background(), login.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if session.RevokedAt == nil || session.RevokeReason == nil || *session.RevokeReason != accountDomain.RevokeReasonReplay {
		t.Fatalf("重放应撤销会话: %+v", session)
	}
	if _, err := h.svc.Refresh(context.Background(), "unknown-token"); !errors.Is(err, accountDomain.ErrRefreshUnknown) {
		t.Fatalf("未知令牌=%v", err)
	}
	if _, err := h.svc.Refresh(context.Background(), ""); !errors.Is(err, accountDomain.ErrRefreshUnknown) {
		t.Fatalf("空令牌=%v", err)
	}
}

func TestLogoutIsIdempotent(t *testing.T) {
	h := newHarness(t, nil)
	h.seedUser(t, "alice", accountDomain.RoleUser, accountDomain.StatusActive)
	login := h.login(t, "alice")
	if err := h.svc.Logout(context.Background(), login.Session.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Logout(context.Background(), login.Session.ID); err != nil {
		t.Fatalf("重复退出必须幂等: %v", err)
	}
	if _, err := h.svc.Authenticate(context.Background(), login.AccessToken); !errors.Is(err, accountDomain.ErrSessionInvalid) {
		t.Fatalf("退出后访问令牌应立即失效: %v", err)
	}
}

func TestAuthenticateUsesCurrentDatabaseState(t *testing.T) {
	h := newHarness(t, nil)
	user := h.seedUser(t, "alice", accountDomain.RoleUser, accountDomain.StatusActive)
	login := h.login(t, "alice")

	identity, err := h.svc.Authenticate(context.Background(), login.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if identity.User.ID != user.ID || identity.Session.ID != login.Session.ID {
		t.Fatalf("身份=%+v", identity)
	}
	if identity.User.Role != accountDomain.RoleUser {
		t.Fatalf("角色应取数据库当前值: %s", identity.User.Role)
	}

	if _, err := h.users.SetRole(context.Background(), user.ID, accountDomain.RoleAdmin, h.svc.now()); err != nil {
		t.Fatal(err)
	}
	promoted, err := h.svc.Authenticate(context.Background(), login.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if promoted.User.Role != accountDomain.RoleAdmin {
		t.Fatalf("角色变更应立即生效: %s", promoted.User.Role)
	}

	if _, err := h.users.SetStatus(context.Background(), user.ID, accountDomain.StatusDisabled, h.svc.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Authenticate(context.Background(), login.AccessToken); !errors.Is(err, accountDomain.ErrSessionInvalid) {
		t.Fatalf("禁用账户应拒绝访问: %v", err)
	}

	if _, err := h.svc.Authenticate(context.Background(), ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("缺少令牌=%v", err)
	}
	if _, err := h.svc.Authenticate(context.Background(), "broken"); !errors.Is(err, accountDomain.ErrSessionInvalid) {
		t.Fatalf("非法令牌=%v", err)
	}
}

func TestUpdateNicknameUsesOptimisticLock(t *testing.T) {
	h := newHarness(t, nil)
	h.seedUser(t, "alice", accountDomain.RoleUser, accountDomain.StatusActive)
	login := h.login(t, "alice")
	identity, err := h.svc.Authenticate(context.Background(), login.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := h.svc.UpdateNickname(context.Background(), identity, "  新昵称  ")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Nickname != "新昵称" {
		t.Fatalf("昵称=%q", updated.Nickname)
	}
	if _, err := h.svc.UpdateNickname(context.Background(), identity, "并发昵称"); !errors.Is(err, accountDomain.ErrVersionConflict) {
		t.Fatalf("使用旧版本应冲突: %v", err)
	}
	if _, err := h.svc.UpdateNickname(context.Background(), identity, ""); !errors.Is(err, accountDomain.ErrInvalidNickname) {
		t.Fatalf("空昵称=%v", err)
	}
}

func TestInitAdminOnlyWhenNoActiveAdmin(t *testing.T) {
	h := newHarness(t, nil)
	created, err := h.admin.InitAdmin(context.Background(), InitAdminInput{Username: "Root", Password: "Abcd123!", OperationID: "op-1"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Role != accountDomain.RoleAdmin || created.Username != "root" {
		t.Fatalf("初始化管理员=%+v", created)
	}
	if len(h.audits.entries) != 1 || h.audits.entries[0].Action != "init_admin" {
		t.Fatalf("审计=%+v", h.audits.entries)
	}
	if _, err := h.admin.InitAdmin(context.Background(), InitAdminInput{Username: "root2", Password: "Abcd123!", OperationID: "op-2"}); !errors.Is(err, ErrAdminAlreadyExists) {
		t.Fatalf("已有管理员时=%v", err)
	}
	if _, err := h.admin.InitAdmin(context.Background(), InitAdminInput{Username: "root3", Password: "abcdefgh", OperationID: "op-3"}); !errors.Is(err, accountDomain.ErrPasswordClasses) {
		t.Fatalf("弱密码=%v", err)
	}
}

func TestSetRoleIsIdempotentAndRevokesSessions(t *testing.T) {
	h := newHarness(t, nil)
	h.seedUser(t, "root_admin", accountDomain.RoleAdmin, accountDomain.StatusActive)
	h.seedUser(t, "alice", accountDomain.RoleUser, accountDomain.StatusActive)
	login := h.login(t, "alice")

	unchanged, changed, err := h.admin.SetRole(context.Background(), SetRoleInput{Username: "alice", Role: accountDomain.RoleUser, OperationID: "op-1"})
	if err != nil || changed {
		t.Fatalf("同值应幂等: changed=%t err=%v", changed, err)
	}
	if unchanged.Role != accountDomain.RoleUser {
		t.Fatalf("角色=%s", unchanged.Role)
	}
	if h.sessions.revokedAll != 0 || len(h.audits.entries) != 0 {
		t.Fatal("同值不得撤销会话或新增审计")
	}
	if _, err := h.svc.Authenticate(context.Background(), login.AccessToken); err != nil {
		t.Fatalf("同值时会话应保持有效: %v", err)
	}

	updated, changed, err := h.admin.SetRole(context.Background(), SetRoleInput{Username: "alice", Role: accountDomain.RoleAdmin, OperationID: "op-2"})
	if err != nil || !changed {
		t.Fatalf("实际变更: changed=%t err=%v", changed, err)
	}
	if updated.Role != accountDomain.RoleAdmin {
		t.Fatalf("角色=%s", updated.Role)
	}
	if h.sessions.revokedAll != 1 || h.sessions.lastReason != accountDomain.RevokeReasonRoleChanged {
		t.Fatalf("实际角色变更应撤销全部会话: %+v", h.sessions)
	}
	if len(h.audits.entries) != 1 || h.audits.entries[0].Action != "set_role" {
		t.Fatalf("审计=%+v", h.audits.entries)
	}
	if _, err := h.svc.Authenticate(context.Background(), login.AccessToken); !errors.Is(err, accountDomain.ErrSessionInvalid) {
		t.Fatalf("角色变更后旧会话应失效: %v", err)
	}

	if _, _, err := h.admin.SetRole(context.Background(), SetRoleInput{Username: "alice", Role: accountDomain.RoleUser, OperationID: "op-3"}); err != nil {
		t.Fatalf("仍有其他管理员时降级应成功: %v", err)
	}
	if _, _, err := h.admin.SetRole(context.Background(), SetRoleInput{Username: "root_admin", Role: accountDomain.RoleUser, OperationID: "op-4"}); !errors.Is(err, accountDomain.ErrLastAdmin) {
		t.Fatalf("降级最后管理员=%v", err)
	}
	if _, _, err := h.admin.SetRole(context.Background(), SetRoleInput{Username: "root_admin", Role: "owner", OperationID: "op-5"}); !errors.Is(err, accountDomain.ErrInvalidRole) {
		t.Fatalf("非法角色=%v", err)
	}
	if _, _, err := h.admin.SetRole(context.Background(), SetRoleInput{Username: "missing", Role: accountDomain.RoleAdmin}); !errors.Is(err, accountDomain.ErrNotFound) {
		t.Fatalf("未知账户=%v", err)
	}
}

func TestSetStatusProtectsLastAdminAndRevokesSessions(t *testing.T) {
	h := newHarness(t, nil)
	h.seedUser(t, "root_admin", accountDomain.RoleAdmin, accountDomain.StatusActive)
	h.seedUser(t, "alice", accountDomain.RoleUser, accountDomain.StatusActive)
	h.login(t, "alice")

	disabled, changed, err := h.admin.SetStatus(context.Background(), SetStatusInput{Username: "alice", Status: accountDomain.StatusDisabled, OperationID: "op-1"})
	if err != nil || !changed || disabled.Status != accountDomain.StatusDisabled {
		t.Fatalf("禁用: %+v changed=%t err=%v", disabled, changed, err)
	}
	if h.sessions.revokedAll != 1 || h.sessions.lastReason != accountDomain.RevokeReasonDisabled {
		t.Fatalf("禁用应撤销全部会话: %+v", h.sessions)
	}
	if len(h.audits.entries) != 1 || h.audits.entries[0].Action != "set_status" {
		t.Fatalf("审计=%+v", h.audits.entries)
	}

	repeated, changed, err := h.admin.SetStatus(context.Background(), SetStatusInput{Username: "alice", Status: accountDomain.StatusDisabled, OperationID: "op-2"})
	if err != nil || changed || repeated.Status != accountDomain.StatusDisabled {
		t.Fatalf("同值应幂等: %+v changed=%t err=%v", repeated, changed, err)
	}
	if _, _, err := h.admin.SetStatus(context.Background(), SetStatusInput{Username: "root_admin", Status: accountDomain.StatusDisabled, OperationID: "op-3"}); !errors.Is(err, accountDomain.ErrLastAdmin) {
		t.Fatalf("禁用最后管理员=%v", err)
	}
}

func TestAdminOperationsTakeLocksInFixedOrder(t *testing.T) {
	h := newHarness(t, nil)
	h.seedUser(t, "alice", accountDomain.RoleUser, accountDomain.StatusActive)
	if _, _, err := h.admin.SetRole(context.Background(), SetRoleInput{Username: "alice", Role: accountDomain.RoleAdmin, OperationID: "op-1"}); err != nil {
		t.Fatal(err)
	}
	if len(h.locks.calls) < 2 || h.locks.calls[0] != "admin_scope" || !strings.HasPrefix(h.locks.calls[1], "user:") {
		t.Fatalf("锁顺序=%v", h.locks.calls)
	}
}
