package integration

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testDatabaseName 返回 DSN 实际生效的库名。
// 不能用 url.Parse(dsn).Path 判断后缀：libpq 风格连接串里的 dbname/database/host 等查询参数
// 会覆盖 path 推导出的取值，只查 path 会把「path 像测试库、实际连到别的库」放过去。
func testDatabaseName(databaseURL string) (string, error) {
	cfg, err := pgconn.ParseConfig(databaseURL)
	if err != nil {
		return "", err
	}
	return cfg.Database, nil
}

// testEnv 是集成测试共享基座：连接专用 _test 库、应用迁移并提供分区清表。
type testEnv struct {
	pool        *pgxpool.Pool
	databaseURL string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	databaseURL := os.Getenv("VELIS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("未设置 VELIS_TEST_DATABASE_URL")
	}
	name, err := testDatabaseName(databaseURL)
	if err != nil {
		t.Fatalf("解析 VELIS_TEST_DATABASE_URL: %v", err)
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatalf("集成测试只允许使用名称以 _test 结尾的数据库，实际连接目标是 %q", name)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	env := &testEnv{pool: pool, databaseURL: databaseURL}
	env.requireTestDatabase(t)
	env.migrate(t)
	return env
}

// requireTestDatabase 是连上之后的第二道闸：迁移与清表之前，再核对一次服务端看到的库名，
// 保证上面那道基于 DSN 的检查没有被任何解析差异绕过。
func (e *testEnv) requireTestDatabase(t *testing.T) {
	t.Helper()
	var name string
	if err := e.pool.QueryRow(context.Background(), "SELECT current_database()").Scan(&name); err != nil {
		t.Fatalf("核对当前数据库: %v", err)
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatalf("集成测试只允许使用名称以 _test 结尾的数据库，当前连接的是 %q", name)
	}
}

// migrate 应用仓库迁移；已是最新版本时不报错，保证测试库结构与应用一致。
func (e *testEnv) migrate(t *testing.T) {
	t.Helper()
	abs, err := filepath.Abs("../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(e.databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", "public")
	parsed.RawQuery = query.Encode()
	m, err := migrate.New("file://"+filepath.ToSlash(abs), parsed.String())
	if err != nil {
		t.Fatalf("初始化迁移器: %v", err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("应用迁移: %v", err)
	}
}

// resetAccounts 清空账户、会话、限流与审计数据；不含 Source/Article。
func (e *testEnv) resetAccounts(t *testing.T) {
	t.Helper()
	e.truncate(t, `TRUNCATE velis.account_audit_logs, velis.login_blocks, velis.login_failure_events,
velis.refresh_tokens, velis.auth_sessions, velis.users RESTART IDENTITY CASCADE`)
}

func (e *testEnv) resetArticles(t *testing.T) {
	t.Helper()
	e.truncate(t, `TRUNCATE velis.async_tasks, velis.consumed_events, velis.outbox_events,
velis.article_asset_references, velis.idempotency_operations,
velis.source_fetch_runs, velis.article_assets, velis.article_versions,
velis.articles, velis.sources RESTART IDENTITY CASCADE`)
}

func (e *testEnv) truncate(t *testing.T, statement string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), statement); err != nil {
		t.Fatal(err)
	}
}

// fixedNow 是集成测试统一使用的固定时刻，避免断言依赖真实时间。
func fixedNow() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) }
