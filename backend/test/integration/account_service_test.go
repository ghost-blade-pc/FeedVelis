package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/security"
)

type accountStack struct {
	service *accountApp.Service
	admin   *accountApp.AdminService
	now     time.Time
}

// newAccountStack 用真实适配器装配账户用例：真实 PostgreSQL 仓储、Argon2id、JWT、HMAC 限流键。
func newAccountStack(t *testing.T, env *testEnv) *accountStack {
	t.Helper()
	ctx := context.Background()
	now := fixedNow()

	hasher, err := security.NewPasswordHasher(security.DefaultArgon2Params(), 1)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := security.NewJWTSigner("velis-api", "velis-web",
		security.SigningKey{KID: "k1", Key: []byte(fixedSecret(1))}, nil, 30*time.Second,
		func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	throttleKeys, err := security.NewThrottleKeys([]byte(fixedSecret(2)))
	if err != nil {
		t.Fatal(err)
	}
	service, err := accountApp.NewService(accountApp.Deps{
		Users:        postgres.NewAccountRepository(env.pool),
		Sessions:     postgres.NewSessionRepository(env.pool),
		Tokens:       postgres.NewRefreshTokenRepository(env.pool),
		Throttle:     postgres.NewThrottleRepository(env.pool),
		Hasher:       hasher,
		Signer:       signer,
		Blocklist:    security.NewCommonPasswordBlocklist(),
		ThrottleKeys: throttleKeys,
		Secrets:      security.NewTokenSecrets(),
		Tx:           postgres.NewTxManager(env.pool),
		Clock:        fixedClock{now: now},
		Policy:       accountDomain.DefaultThrottlePolicy(),

		AccessTTL:           15 * time.Minute,
		SessionTTL:          accountDomain.SessionTTL,
		RegistrationEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := accountApp.NewAdminService(accountApp.AdminDeps{
		Users:     postgres.NewAccountRepository(env.pool),
		Sessions:  postgres.NewSessionRepository(env.pool),
		Audits:    postgres.NewAuditRepository(env.pool),
		Locks:     postgres.NewAccountTxLocks(),
		Hasher:    hasher,
		Blocklist: security.NewCommonPasswordBlocklist(),
		Tx:        postgres.NewTxManager(env.pool),
		Clock:     fixedClock{now: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = ctx
	return &accountStack{service: service, admin: admin, now: now}
}

func fixedSecret(fill byte) []byte {
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = fill
	}
	return secret
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func TestAccountLifecycleAgainstPostgres(t *testing.T) {
	env := newTestEnv(t)
	env.resetAccounts(t)
	stack := newAccountStack(t, env)
	ctx := context.Background()

	user, err := stack.service.Register(ctx, accountApp.RegisterInput{Username: "Alice", Password: "Abcd123!"})
	if err != nil {
		t.Fatalf("注册: %v", err)
	}
	if user.Username != "alice" || user.Role != accountDomain.RoleUser {
		t.Fatalf("注册结果 = %+v", user)
	}
	if _, err := stack.service.Register(ctx, accountApp.RegisterInput{Username: "alice", Password: "Abcd123!"}); !errors.Is(err, accountDomain.ErrUsernameTaken) {
		t.Fatalf("重名注册 = %v", err)
	}

	login, err := stack.service.Login(ctx, accountApp.LoginInput{Username: "alice", Password: "Abcd123!", ClientIP: "203.0.113.10"})
	if err != nil {
		t.Fatalf("登录: %v", err)
	}
	identity, err := stack.service.Authenticate(ctx, login.AccessToken)
	if err != nil {
		t.Fatalf("身份解析: %v", err)
	}
	if identity.User.ID != user.ID || identity.Session.ID != login.Session.ID {
		t.Fatalf("身份 = %+v", identity)
	}

	renamed, err := stack.service.UpdateNickname(ctx, identity, "新昵称")
	if err != nil || renamed.Nickname != "新昵称" {
		t.Fatalf("改昵称: %+v err=%v", renamed, err)
	}
	if _, err := stack.service.UpdateNickname(ctx, identity, "旧版本"); !errors.Is(err, accountDomain.ErrVersionConflict) {
		t.Fatalf("旧版本改昵称 = %v", err)
	}

	refreshed, err := stack.service.Refresh(ctx, login.RefreshToken)
	if err != nil {
		t.Fatalf("刷新: %v", err)
	}
	if refreshed.Session.ID != login.Session.ID || refreshed.RefreshToken == login.RefreshToken {
		t.Fatalf("刷新结果 = %+v", refreshed)
	}
	if _, err := stack.service.Refresh(ctx, login.RefreshToken); !errors.Is(err, accountDomain.ErrRefreshReplay) {
		t.Fatalf("重放 = %v", err)
	}
	if _, err := stack.service.Authenticate(ctx, refreshed.AccessToken); !errors.Is(err, accountDomain.ErrSessionInvalid) {
		t.Fatalf("重放撤销后会话必须失效: %v", err)
	}
	if _, err := stack.service.Refresh(ctx, refreshed.RefreshToken); !errors.Is(err, accountDomain.ErrSessionInvalid) {
		t.Fatalf("已撤销会话不得再刷新: %v", err)
	}

	second, err := stack.service.Login(ctx, accountApp.LoginInput{Username: "alice", Password: "Abcd123!", ClientIP: "203.0.113.10"})
	if err != nil {
		t.Fatalf("二次登录: %v", err)
	}
	if second.Session.ID == login.Session.ID {
		t.Fatal("每次登录必须建立独立会话")
	}
	if err := stack.service.Logout(ctx, second.Session.ID); err != nil {
		t.Fatalf("退出: %v", err)
	}
	if err := stack.service.Logout(ctx, second.Session.ID); err != nil {
		t.Fatalf("重复退出必须幂等: %v", err)
	}
	if _, err := stack.service.Authenticate(ctx, second.AccessToken); !errors.Is(err, accountDomain.ErrSessionInvalid) {
		t.Fatalf("退出后应立即失效: %v", err)
	}
}

func TestLoginThrottleAgainstPostgres(t *testing.T) {
	env := newTestEnv(t)
	env.resetAccounts(t)
	stack := newAccountStack(t, env)
	ctx := context.Background()

	if _, err := stack.service.Register(ctx, accountApp.RegisterInput{Username: "bob", Password: "Abcd123!"}); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if _, err := stack.service.Login(ctx, accountApp.LoginInput{Username: "bob", Password: "Wrong123!", ClientIP: "198.51.100.5"}); !errors.Is(err, accountApp.ErrInvalidCredentials) {
			t.Fatalf("第 %d 次失败 = %v", i+1, err)
		}
	}
	_, err := stack.service.Login(ctx, accountApp.LoginInput{Username: "bob", Password: "Abcd123!", ClientIP: "198.51.100.5"})
	var limited *accountApp.RateLimitedError
	if !errors.As(err, &limited) || limited.RetryAfter <= 0 {
		t.Fatalf("限制期内必须拒绝: %v", err)
	}

	// 其他 IP 上的同名账户同样受限，账号维度跨请求生效。
	_, err = stack.service.Login(ctx, accountApp.LoginInput{Username: "bob", Password: "Abcd123!", ClientIP: "198.51.100.6"})
	if !errors.As(err, &limited) {
		t.Fatalf("账号维度限制应跨 IP 生效: %v", err)
	}
}

func TestAdminMaintenanceAgainstPostgres(t *testing.T) {
	env := newTestEnv(t)
	env.resetAccounts(t)
	stack := newAccountStack(t, env)
	ctx := context.Background()

	admin, err := stack.admin.InitAdmin(ctx, accountApp.InitAdminInput{Username: "root", Password: "Abcd123!", OperationID: mustUUID(t).String()})
	if err != nil {
		t.Fatalf("初始化管理员: %v", err)
	}
	if admin.Role != accountDomain.RoleAdmin {
		t.Fatalf("角色 = %s", admin.Role)
	}
	if _, err := stack.admin.InitAdmin(ctx, accountApp.InitAdminInput{Username: "root2", Password: "Abcd123!", OperationID: mustUUID(t).String()}); !errors.Is(err, accountApp.ErrAdminAlreadyExists) {
		t.Fatalf("重复初始化 = %v", err)
	}

	member, err := stack.service.Register(ctx, accountApp.RegisterInput{Username: "carol", Password: "Abcd123!"})
	if err != nil {
		t.Fatal(err)
	}
	login, err := stack.service.Login(ctx, accountApp.LoginInput{Username: "carol", Password: "Abcd123!", ClientIP: "203.0.113.20"})
	if err != nil {
		t.Fatal(err)
	}
	promoted, changed, err := stack.admin.SetRole(ctx, accountApp.SetRoleInput{Username: "carol", Role: accountDomain.RoleAdmin, OperationID: mustUUID(t).String()})
	if err != nil || !changed || promoted.Role != accountDomain.RoleAdmin {
		t.Fatalf("提权: %+v changed=%t err=%v", promoted, changed, err)
	}
	if _, err := stack.service.Authenticate(ctx, login.AccessToken); !errors.Is(err, accountDomain.ErrSessionInvalid) {
		t.Fatalf("角色变更应撤销该账户全部会话: %v", err)
	}
	if _, _, err := stack.admin.SetRole(ctx, accountApp.SetRoleInput{Username: "carol", Role: accountDomain.RoleAdmin, OperationID: mustUUID(t).String()}); err != nil {
		t.Fatalf("同值变更应幂等: %v", err)
	}

	if _, _, err := stack.admin.SetStatus(ctx, accountApp.SetStatusInput{Username: "carol", Status: accountDomain.StatusDisabled, OperationID: mustUUID(t).String()}); err != nil {
		t.Fatalf("禁用普通管理员: %v", err)
	}
	if _, _, err := stack.admin.SetStatus(ctx, accountApp.SetStatusInput{Username: "root", Status: accountDomain.StatusDisabled, OperationID: mustUUID(t).String()}); !errors.Is(err, accountDomain.ErrLastAdmin) {
		t.Fatalf("禁用最后管理员 = %v", err)
	}
	if _, _, err := stack.admin.SetRole(ctx, accountApp.SetRoleInput{Username: "root", Role: accountDomain.RoleUser, OperationID: mustUUID(t).String()}); !errors.Is(err, accountDomain.ErrLastAdmin) {
		t.Fatalf("降级最后管理员 = %v", err)
	}
	if _, err := stack.service.Login(ctx, accountApp.LoginInput{Username: "carol", Password: "Abcd123!", ClientIP: "203.0.113.21"}); !errors.Is(err, accountApp.ErrInvalidCredentials) {
		t.Fatalf("禁用后登录 = %v", err)
	}
	_ = member
}
