package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

const testPasswordHash = "$argon2id$v=19$m=19456,t=2,p=1$Y2FsY2l1bXNhbHQ$0000000000000000000000000000000000000000000"

func createTestUser(t *testing.T, repo *postgres.AccountRepository, username string, role accountDomain.Role, now time.Time) accountDomain.User {
	t.Helper()
	id, err := accountDomain.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	user, err := repo.Create(context.Background(), accountDomain.User{
		ID:           id,
		Username:     username,
		Nickname:     username,
		PasswordHash: testPasswordHash,
		Role:         role,
		Status:       accountDomain.StatusActive,
		CreatedAt:    now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func TestAccountCreateUniquenessUnderConcurrency(t *testing.T) {
	env := newTestEnv(t)
	env.resetAccounts(t)
	repo := postgres.NewAccountRepository(env.pool)
	now := fixedNow()

	created := createTestUser(t, repo, "alice", accountDomain.RoleUser, now)
	if created.Version != 1 {
		t.Fatalf("新建版本 = %d", created.Version)
	}
	if _, err := repo.GetByUsername(context.Background(), "alice"); err != nil {
		t.Fatalf("按规范化用户名读取失败: %v", err)
	}

	const goroutines = 4
	var (
		group    sync.WaitGroup
		mu       sync.Mutex
		success  int
		conflict int
	)
	for range goroutines {
		group.Add(1)
		go func() {
			defer group.Done()
			id, err := accountDomain.NewUUID()
			if err != nil {
				t.Errorf("生成 UUID: %v", err)
				return
			}
			_, err = repo.Create(context.Background(), accountDomain.User{
				ID: id, Username: "bob", Nickname: "bob", PasswordHash: testPasswordHash,
				Role: accountDomain.RoleAdmin, Status: accountDomain.StatusActive, CreatedAt: now,
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				success++
			case errors.Is(err, accountDomain.ErrUsernameTaken):
				conflict++
			default:
				t.Errorf("非预期错误: %v", err)
			}
		}()
	}
	group.Wait()
	if success != 1 || conflict != goroutines-1 {
		t.Fatalf("并发创建 success=%d conflict=%d", success, conflict)
	}
	admin, err := repo.GetByUsername(context.Background(), "bob")
	if err != nil {
		t.Fatal(err)
	}
	if admin.Role != accountDomain.RoleAdmin {
		t.Fatalf("角色 = %s", admin.Role)
	}
}

func TestAccountUpdatesAndActiveAdminCount(t *testing.T) {
	env := newTestEnv(t)
	env.resetAccounts(t)
	repo := postgres.NewAccountRepository(env.pool)
	ctx := context.Background()
	now := fixedNow()

	admin := createTestUser(t, repo, "root_admin", accountDomain.RoleAdmin, now)
	if count, err := repo.CountActiveAdmins(ctx); err != nil || count != 1 {
		t.Fatalf("管理员数量 = %d err = %v", count, err)
	}

	updated, err := repo.UpdateNickname(ctx, admin.ID, "新昵称", admin.Version, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Nickname != "新昵称" || updated.Version != admin.Version+1 {
		t.Fatalf("更新后 = %+v", updated)
	}
	if _, err := repo.UpdateNickname(ctx, admin.ID, "并发昵称", admin.Version, now); !errors.Is(err, accountDomain.ErrVersionConflict) {
		t.Fatalf("版本冲突未被拒绝: %v", err)
	}

	demoted, err := repo.SetRole(ctx, admin.ID, accountDomain.RoleUser, now)
	if err != nil {
		t.Fatal(err)
	}
	if demoted.Role != accountDomain.RoleUser {
		t.Fatalf("角色 = %s", demoted.Role)
	}
	if count, err := repo.CountActiveAdmins(ctx); err != nil || count != 0 {
		t.Fatalf("降级后管理员数量 = %d err = %v", count, err)
	}

	disabled, err := repo.SetStatus(ctx, admin.ID, accountDomain.StatusDisabled, now)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Status != accountDomain.StatusDisabled {
		t.Fatalf("状态 = %s", disabled.Status)
	}
	if _, err := repo.GetByID(ctx, accountDomain.UUID{}); !errors.Is(err, accountDomain.ErrNotFound) {
		t.Fatalf("未知账户应返回 ErrNotFound: %v", err)
	}
}

func TestRefreshRotationReplayAndUnknownDigest(t *testing.T) {
	env := newTestEnv(t)
	env.resetAccounts(t)
	repo := postgres.NewAccountRepository(env.pool)
	sessions := postgres.NewSessionRepository(env.pool)
	tokens := postgres.NewRefreshTokenRepository(env.pool)
	ctx := context.Background()
	now := fixedNow()

	user := createTestUser(t, repo, "alice", accountDomain.RoleUser, now)
	sessionID := mustUUID(t)
	session := accountDomain.NewSession(sessionID, user.ID, now, accountDomain.SessionTTL)
	if err := sessions.Create(ctx, session); err != nil {
		t.Fatal(err)
	}

	first, err := session.NewRefreshToken(mustUUID(t), [32]byte{1}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := tokens.Create(ctx, first); err != nil {
		t.Fatal(err)
	}

	second, err := session.NewRefreshToken(mustUUID(t), [32]byte{2}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := tokens.Rotate(ctx, first.Digest, second, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("首次轮换失败: %v", err)
	}
	if rotated.ID != sessionID || !rotated.LastRefreshedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("轮换后会话 = %+v", rotated)
	}

	if _, err := tokens.Rotate(ctx, [32]byte{9}, second, now.Add(2*time.Minute)); !errors.Is(err, accountDomain.ErrRefreshUnknown) {
		t.Fatalf("未知摘要 = %v", err)
	}
	if current, err := sessions.GetByID(ctx, sessionID); err != nil || current.RevokedAt != nil {
		t.Fatalf("未知摘要不得影响会话: %+v err=%v", current, err)
	}

	if _, err := tokens.Rotate(ctx, first.Digest, second, now.Add(3*time.Minute)); !errors.Is(err, accountDomain.ErrRefreshReplay) {
		t.Fatalf("重放 = %v", err)
	}
	revoked, err := sessions.GetByID(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.RevokedAt == nil || revoked.RevokeReason == nil || *revoked.RevokeReason != accountDomain.RevokeReasonReplay {
		t.Fatalf("重放应撤销会话: %+v", revoked)
	}
}

func TestRefreshRotationIsSingleWinnerUnderConcurrency(t *testing.T) {
	env := newTestEnv(t)
	env.resetAccounts(t)
	repo := postgres.NewAccountRepository(env.pool)
	sessions := postgres.NewSessionRepository(env.pool)
	tokens := postgres.NewRefreshTokenRepository(env.pool)
	ctx := context.Background()
	now := fixedNow()

	user := createTestUser(t, repo, "alice", accountDomain.RoleUser, now)
	session := accountDomain.NewSession(mustUUID(t), user.ID, now, accountDomain.SessionTTL)
	if err := sessions.Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	first, err := session.NewRefreshToken(mustUUID(t), [32]byte{5}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := tokens.Create(ctx, first); err != nil {
		t.Fatal(err)
	}

	const goroutines = 3
	var (
		group   sync.WaitGroup
		mu      sync.Mutex
		success int
		replay  int
	)
	for i := range goroutines {
		group.Add(1)
		go func() {
			defer group.Done()
			next, err := session.NewRefreshToken(mustUUID(t), [32]byte{byte(10 + i)}, now)
			if err != nil {
				t.Errorf("构造后继令牌: %v", err)
				return
			}
			_, err = tokens.Rotate(context.Background(), first.Digest, next, now)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				success++
			case errors.Is(err, accountDomain.ErrRefreshReplay), errors.Is(err, accountDomain.ErrSessionInvalid):
				replay++
			default:
				t.Errorf("非预期错误: %v", err)
			}
		}()
	}
	group.Wait()
	if success != 1 {
		t.Fatalf("并发轮换成功次数 = %d", success)
	}
	if replay != goroutines-1 {
		t.Fatalf("并发轮换其余结果 = %d", replay)
	}
}

func TestSessionRevocationIsIdempotent(t *testing.T) {
	env := newTestEnv(t)
	env.resetAccounts(t)
	repo := postgres.NewAccountRepository(env.pool)
	sessions := postgres.NewSessionRepository(env.pool)
	ctx := context.Background()
	now := fixedNow()

	user := createTestUser(t, repo, "alice", accountDomain.RoleUser, now)
	first := accountDomain.NewSession(mustUUID(t), user.ID, now, accountDomain.SessionTTL)
	second := accountDomain.NewSession(mustUUID(t), user.ID, now, accountDomain.SessionTTL)
	for _, session := range []accountDomain.Session{first, second} {
		if err := sessions.Create(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	if err := sessions.Revoke(ctx, first.ID, accountDomain.RevokeReasonLogout, now); err != nil {
		t.Fatal(err)
	}
	if err := sessions.Revoke(ctx, first.ID, accountDomain.RevokeReasonLogout, now.Add(time.Minute)); err != nil {
		t.Fatalf("重复撤销必须幂等: %v", err)
	}
	revoked, err := sessions.GetByID(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.RevokedAt == nil {
		t.Fatal("会话应已撤销")
	}
	untouched, err := sessions.GetByID(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if untouched.RevokedAt != nil {
		t.Fatal("其他设备会话不得受影响")
	}
	affected, err := sessions.RevokeAllForUser(ctx, user.ID, accountDomain.RevokeReasonDisabled, now)
	if err != nil {
		t.Fatal(err)
	}
	if affected != 1 {
		t.Fatalf("禁用撤销会话数 = %d", affected)
	}
}

func TestThrottleRollingWindowAndBlocking(t *testing.T) {
	env := newTestEnv(t)
	env.resetAccounts(t)
	throttle := postgres.NewThrottleRepository(env.pool)
	ctx := context.Background()
	policy := accountDomain.DefaultThrottlePolicy()
	now := fixedNow()
	key := make([]byte, 32)
	key[0] = 7

	for i := range policy.AccountLimit - 1 {
		blocked, err := throttle.RecordFailure(ctx, accountDomain.DimensionAccount, key, now.Add(time.Duration(i)*time.Second), policy)
		if err != nil {
			t.Fatal(err)
		}
		if blocked != nil {
			t.Fatalf("第 %d 次失败不应触发限制", i+1)
		}
	}
	blockedAt := now.Add(time.Minute)
	blocked, err := throttle.RecordFailure(ctx, accountDomain.DimensionAccount, key, blockedAt, policy)
	if err != nil {
		t.Fatal(err)
	}
	if blocked == nil || !blocked.Equal(policy.BlockedUntil(blockedAt)) {
		t.Fatalf("第 5 次失败应触发限制: %v", blocked)
	}

	active, err := throttle.ActiveBlock(ctx, accountDomain.DimensionAccount, key, blockedAt.Add(time.Minute))
	if err != nil || active == nil || !active.Equal(*blocked) {
		t.Fatalf("限制期内应返回同一截止时间: %v err=%v", active, err)
	}

	var failuresBefore int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.login_failure_events`).Scan(&failuresBefore); err != nil {
		t.Fatal(err)
	}
	extended, err := throttle.RecordFailure(ctx, accountDomain.DimensionAccount, key, blockedAt.Add(2*time.Minute), policy)
	if err != nil {
		t.Fatal(err)
	}
	if extended == nil || !extended.Equal(*blocked) {
		t.Fatalf("限制期间不得延长截止时间: %v", extended)
	}
	var failuresAfter int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.login_failure_events`).Scan(&failuresAfter); err != nil {
		t.Fatal(err)
	}
	if failuresAfter != failuresBefore {
		t.Fatalf("限制期间不得新增失败记录: %d → %d", failuresBefore, failuresAfter)
	}

	other := make([]byte, 32)
	other[0] = 8
	if active, err := throttle.ActiveBlock(ctx, accountDomain.DimensionAccount, other, blockedAt); err != nil || active != nil {
		t.Fatalf("其他维度键不得受限: %v err=%v", active, err)
	}
	expired, err := throttle.ActiveBlock(ctx, accountDomain.DimensionAccount, key, policy.BlockedUntil(blockedAt).Add(time.Second))
	if err != nil || expired != nil {
		t.Fatalf("限制到期应自动恢复: %v err=%v", expired, err)
	}
}

func TestThrottleCountsAtomicallyUnderConcurrency(t *testing.T) {
	env := newTestEnv(t)
	env.resetAccounts(t)
	throttle := postgres.NewThrottleRepository(env.pool)
	ctx := context.Background()
	policy := accountDomain.DefaultThrottlePolicy()
	policy.IPLimit = 5
	now := fixedNow()
	key := make([]byte, 32)
	key[0] = 9

	const goroutines = 6
	var (
		group   sync.WaitGroup
		mu      sync.Mutex
		blocked int
	)
	for i := range goroutines {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := throttle.RecordFailure(context.Background(), accountDomain.DimensionIP, key,
				now.Add(time.Duration(i)*time.Millisecond), policy)
			if err != nil {
				t.Errorf("并发记录失败: %v", err)
				return
			}
			if result != nil {
				mu.Lock()
				blocked++
				mu.Unlock()
			}
		}()
	}
	group.Wait()

	var failures int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.login_failure_events WHERE dimension = 'ip'`).Scan(&failures); err != nil {
		t.Fatal(err)
	}
	if failures != policy.IPLimit {
		t.Fatalf("并发写入失败记录 = %d，应恰好等于阈值 %d", failures, policy.IPLimit)
	}
	if blocked == 0 {
		t.Fatal("达到 IP 阈值后应触发限制")
	}
}

func TestAuditRecordAndCleanupRetention(t *testing.T) {
	env := newTestEnv(t)
	env.resetAccounts(t)
	repo := postgres.NewAccountRepository(env.pool)
	sessions := postgres.NewSessionRepository(env.pool)
	tokens := postgres.NewRefreshTokenRepository(env.pool)
	audits := postgres.NewAuditRepository(env.pool)
	cleanup := postgres.NewCleanupRepository(env.pool)
	tx := postgres.NewTxManager(env.pool)
	ctx := context.Background()
	now := fixedNow()

	user := createTestUser(t, repo, "alice", accountDomain.RoleUser, now)
	from := accountDomain.RoleUser
	to := accountDomain.RoleAdmin
	if err := tx.WithinTransaction(ctx, func(ctx context.Context) error {
		if _, err := repo.SetRole(ctx, user.ID, to, now); err != nil {
			return err
		}
		return audits.Record(ctx, accountDomain.AuditLog{
			Action: "set_role", TargetUserID: user.ID, TargetUsername: user.Username,
			FromRole: &from, ToRole: &to, Source: "local_cli", OperationID: mustUUID(t).String(), OccurredAt: now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	var (
		action string
		source string
	)
	if err := env.pool.QueryRow(ctx, `SELECT action, source FROM velis.account_audit_logs WHERE target_user_id = $1::uuid`, user.ID.String()).Scan(&action, &source); err != nil {
		t.Fatal(err)
	}
	if action != "set_role" || source != "local_cli" {
		t.Fatalf("审计记录 = %s/%s", action, source)
	}

	session := accountDomain.NewSession(mustUUID(t), user.ID, now, accountDomain.SessionTTL)
	if err := sessions.Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	token, err := session.NewRefreshToken(mustUUID(t), [32]byte{3}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := tokens.Create(ctx, token); err != nil {
		t.Fatal(err)
	}

	if err := sessions.Revoke(ctx, session.ID, accountDomain.RevokeReasonLogout, now); err != nil {
		t.Fatal(err)
	}
	// 保留期内的数据不得被清理。
	if removed, err := cleanup.DeleteExpiredSessions(ctx, now.Add(-time.Hour), 100); err != nil || removed != 0 {
		t.Fatalf("保留期内会话被清理: %d err=%v", removed, err)
	}
	// 达到保留期后按批删除：令牌先于会话，用户与审计始终不受影响。
	removedTokens, err := cleanup.DeleteExpiredRefreshTokens(ctx, session.ExpiresAt.Add(time.Hour), 100)
	if err != nil || removedTokens != 1 {
		t.Fatalf("过期令牌清理 = %d err=%v", removedTokens, err)
	}
	removed, err := cleanup.DeleteExpiredSessions(ctx, now.Add(time.Hour), 100)
	if err != nil || removed != 1 {
		t.Fatalf("过期会话清理 = %d err=%v", removed, err)
	}
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.login_failure_events (dimension, lookup_key, occurred_at)
VALUES ('ip', $1, $2)`, make([]byte, 32), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if removed, err := cleanup.DeleteStaleFailures(ctx, now.Add(-time.Hour), 100); err != nil || removed != 1 {
		t.Fatalf("过期失败事件清理 = %d err=%v", removed, err)
	}
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.login_blocks (dimension, lookup_key, blocked_until, updated_at)
VALUES ('ip', $1, $2, $3)`, make([]byte, 32), now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if removed, err := cleanup.DeleteExpiredBlocks(ctx, now, 100); err != nil || removed != 1 {
		t.Fatalf("过期限制清理 = %d err=%v", removed, err)
	}
	var users, auditRows int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.account_audit_logs`).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if users != 1 || auditRows != 1 {
		t.Fatalf("用户 = %d 审计 = %d，均不得被清理", users, auditRows)
	}
	if _, err := repo.GetByID(ctx, user.ID); err != nil {
		t.Fatalf("用户不得被清理: %v", err)
	}
}

func TestAdminScopeAdvisoryLockBlocksDuplicateInitialization(t *testing.T) {
	env := newTestEnv(t)
	env.resetAccounts(t)
	repo := postgres.NewAccountRepository(env.pool)
	locks := postgres.NewAccountTxLocks()
	tx := postgres.NewTxManager(env.pool)
	now := fixedNow()

	createAdminIfNone := func(username string) error {
		return tx.WithinTransaction(context.Background(), func(ctx context.Context) error {
			if err := locks.LockAdminScope(ctx); err != nil {
				return err
			}
			count, err := repo.CountActiveAdmins(ctx)
			if err != nil {
				return err
			}
			if count > 0 {
				return nil
			}
			id, err := accountDomain.NewUUID()
			if err != nil {
				return err
			}
			_, err = repo.Create(ctx, accountDomain.User{
				ID: id, Username: username, Nickname: username, PasswordHash: testPasswordHash,
				Role: accountDomain.RoleAdmin, Status: accountDomain.StatusActive, CreatedAt: now,
			})
			return err
		})
	}

	var group sync.WaitGroup
	for _, username := range []string{"admin_one", "admin_two"} {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := createAdminIfNone(username); err != nil {
				t.Errorf("并发初始化: %v", err)
			}
		}()
	}
	group.Wait()

	if count, err := repo.CountActiveAdmins(context.Background()); err != nil || count != 1 {
		t.Fatalf("并发初始化后管理员数量 = %d err=%v", count, err)
	}
	if err := locks.LockAdminScope(context.Background()); err == nil {
		t.Fatal("事务外获取管理员锁必须失败")
	}
	if _, err := locks.LockUser(context.Background(), accountDomain.UUID{}); err == nil {
		t.Fatal("事务外获取用户行锁必须失败")
	}
}

func mustUUID(t *testing.T) accountDomain.UUID {
	t.Helper()
	id, err := accountDomain.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
