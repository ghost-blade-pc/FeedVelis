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
)

func TestI2MigrationUpgradeDowngradeAndGuards(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.resetArticles(t)
	env.resetAccounts(t)

	runner := newMigrationRunner(t, env.databaseURL)
	defer func() {
		if err := runner.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			t.Errorf("清理时恢复到最新迁移: %v", err)
		}
	}()
	if err := runner.Steps(-3); err != nil {
		t.Fatalf("回到 I2 前结构: %v", err)
	}
	seedLegacyArticles(t, env)
	if err := runner.Steps(1); err != nil {
		t.Fatalf("升级 I2 结构: %v", err)
	}

	assertI2Catalog(t, env)
	assertLegacyUpgrade(t, env)

	if err := runner.Steps(-1); err != nil {
		t.Fatalf("纯历史 RSS 数据应可降级: %v", err)
	}
	var status, title, rawContent string
	if err := env.pool.QueryRow(ctx, `SELECT a.status, a.title, c.raw_content
FROM velis.articles a JOIN velis.article_contents c ON c.article_id = a.id WHERE a.id = 73`).
		Scan(&status, &title, &rawContent); err != nil {
		t.Fatalf("读取降级后的历史文章: %v", err)
	}
	if status != "hidden" || title != "隐藏历史" || rawContent != "<p>raw-hidden</p>" {
		t.Fatalf("降级结果 = %q/%q/%q", status, title, rawContent)
	}
	if err := runner.Steps(1); err != nil {
		t.Fatalf("重新升级 I2 结构: %v", err)
	}

	downSQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000004_unify_content_supply.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	seedI2User(t, env)
	assertGuardRejects(t, env, downSQL, "用户投稿")
	if _, err := env.pool.Exec(ctx, `DELETE FROM velis.articles WHERE id = 1001; DELETE FROM velis.users WHERE username = 'migration_user'`); err != nil {
		t.Fatal(err)
	}

	seedAsset(t, env)
	assertGuardRejects(t, env, downSQL, "文章资产")
	if _, err := env.pool.Exec(ctx, `DELETE FROM velis.article_assets; DELETE FROM velis.users WHERE username = 'asset_user'`); err != nil {
		t.Fatal(err)
	}

	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.article_versions (
article_id, revision_no, title, raw_description, raw_content, sanitized_html, plain_text,
excerpt, language, content_hash, sanitizer_version, created_at)
SELECT article_id, 2, title || ' v2', raw_description, raw_content, sanitized_html, plain_text,
excerpt, language, repeat('f', 64), sanitizer_version, created_at + interval '1 second'
FROM velis.article_versions WHERE article_id = 41 AND revision_no = 1`); err != nil {
		t.Fatal(err)
	}
	assertGuardRejects(t, env, downSQL, "多版本文章")
	assertI2Cleanup(t, env)
}

func assertI2Cleanup(t *testing.T, env *testEnv) {
	t.Helper()
	ctx := context.Background()
	_, err := env.pool.Exec(ctx, `INSERT INTO velis.users
(id, username, nickname, password_hash, role, status, created_at, updated_at)
VALUES ('30000000-0000-0000-0000-000000000001', 'cleanup_user', '清理用户', 'hash', 'user', 'active', now(), now());
INSERT INTO velis.article_assets (id, owner_user_id, bound_article_id, object_key, status,
content_type, size_bytes, width, height, checksum, quota_counted_at, confirmed_at, created_at, updated_at)
VALUES ('30000000-0000-0000-0000-000000000002', '30000000-0000-0000-0000-000000000001', 41,
'article-assets/cleanup/original', 'ready', 'image/png', 128, 1, 1, 'checksum', now(), now(), now(), now());
INSERT INTO velis.article_asset_references (article_version_id, asset_id, created_at)
SELECT current_revision_id, '30000000-0000-0000-0000-000000000002', now() FROM velis.articles WHERE id = 41;
INSERT INTO velis.idempotency_operations (actor_user_id, operation, idempotency_key,
request_digest, status, created_at, expires_at)
VALUES ('30000000-0000-0000-0000-000000000001', 'cleanup-test',
'30000000-0000-0000-0000-000000000003', decode(repeat('ab', 32), 'hex'), 'pending', now(), now() + interval '24 hours');
INSERT INTO velis.source_fetch_runs (id, source_id, trigger, status, lease_generation,
started_at, completed_at, created_at)
VALUES ('30000000-0000-0000-0000-000000000004', 7, 'scheduled', 'succeeded', 1, now(), now(), now())`)
	if err != nil {
		t.Fatalf("准备 I2 清理夹具: %v", err)
	}

	// 此处刻意停留在 v4，可靠异步表尚不存在，不能调用面向最新 schema 的 resetArticles。
	env.truncate(t, `TRUNCATE velis.article_asset_references, velis.idempotency_operations,
velis.source_fetch_runs, velis.article_assets, velis.article_versions,
velis.articles, velis.sources RESTART IDENTITY CASCADE`)
	var remaining int
	if err := env.pool.QueryRow(ctx, `SELECT
(SELECT count(*) FROM velis.article_asset_references) +
(SELECT count(*) FROM velis.article_assets) +
(SELECT count(*) FROM velis.idempotency_operations) +
(SELECT count(*) FROM velis.source_fetch_runs) +
(SELECT count(*) FROM velis.article_versions) +
(SELECT count(*) FROM velis.articles) +
(SELECT count(*) FROM velis.sources)`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("I2 清理后仍有 %d 条记录", remaining)
	}
	if _, err := env.pool.Exec(ctx, `DELETE FROM velis.users WHERE username = 'cleanup_user'`); err != nil {
		t.Fatal(err)
	}
}

func seedLegacyArticles(t *testing.T, env *testEnv) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.sources (
id, feed_url, normalized_feed_url, site_url, title, status, next_fetch_at,
consecutive_failures, created_at, updated_at)
OVERRIDING SYSTEM VALUE VALUES (7, 'https://example.com/feed', 'https://example.com/feed',
'https://example.com', '历史来源', 'active', $1, 0, $1, $1)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.articles (
id, source_id, dedupe_key, source_item_id, canonical_url, title, author_name, excerpt,
language, source_published_at, discovered_at, source_updated_at, content_hash, status,
last_seen_at, created_at, updated_at)
OVERRIDING SYSTEM VALUE VALUES
(41, 7, $1, 'item-41', 'https://example.com/41', '公开历史', '作者甲', '摘要甲', 'zh-CN', NULL, $3, NULL, $2, 'published', $3, $3, $3),
(73, 7, $4, 'item-73', 'https://example.com/73', '隐藏历史', NULL, '摘要乙', 'en', $3, $5, NULL, $6, 'hidden', $5, $5, $5)`,
		strings.Repeat("a", 64), strings.Repeat("b", 64), now,
		strings.Repeat("c", 64), now.Add(time.Hour), strings.Repeat("d", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.article_contents (
article_id, raw_description, raw_content, sanitized_html, plain_text, sanitizer_version, created_at, updated_at)
VALUES (41, 'desc-public', '<p>raw-public</p>', '<p>clean-public</p>', 'plain-public', 2, $1, $1),
(73, 'desc-hidden', '<p>raw-hidden</p>', '<p>clean-hidden</p>', 'plain-hidden', 3, $2, $2)`, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func assertI2Catalog(t *testing.T, env *testEnv) {
	t.Helper()
	ctx := context.Background()
	for _, table := range []string{"article_versions", "article_assets", "article_asset_references", "idempotency_operations", "source_fetch_runs"} {
		var exists bool
		if err := env.pool.QueryRow(ctx, `SELECT to_regclass('velis.' || $1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Fatalf("I2 表 %s 不存在: %v", table, err)
		}
	}
	for _, constraint := range []string{"articles_origin_identity_check", "articles_current_revision_fk", "articles_visibility_state_check"} {
		var exists bool
		if err := env.pool.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM pg_constraint WHERE connamespace = 'velis'::regnamespace AND conname = $1)`, constraint).Scan(&exists); err != nil || !exists {
			t.Fatalf("I2 约束 %s 不存在: %v", constraint, err)
		}
	}
	for _, index := range []string{"articles_latest_idx", "article_assets_pending_cleanup_idx", "source_fetch_runs_running_idx"} {
		var partial bool
		if err := env.pool.QueryRow(ctx, `SELECT i.indpred IS NOT NULL
FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
WHERE c.relnamespace = 'velis'::regnamespace AND c.relname = $1`, index).Scan(&partial); err != nil || !partial {
			t.Fatalf("I2 部分索引 %s 无效: partial=%t err=%v", index, partial, err)
		}
	}
	var nextID int64
	if err := env.pool.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('velis.articles', 'id'))`).Scan(&nextID); err != nil {
		t.Fatal(err)
	}
	if nextID != 74 {
		t.Fatalf("articles identity sequence 下一值 = %d，期望 74", nextID)
	}
}

func assertLegacyUpgrade(t *testing.T, env *testEnv) {
	t.Helper()
	rows, err := env.pool.Query(context.Background(), `SELECT a.id, a.origin_type, a.status,
a.published_at, a.current_revision_id, a.lock_version, a.offline_reason,
v.revision_no, v.title, v.raw_content, v.sanitized_html, v.plain_text, v.excerpt,
v.language, v.content_hash
FROM velis.articles a JOIN velis.article_versions v ON v.id = a.current_revision_id ORDER BY a.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, currentRevision, lockVersion int64
		var revision int
		var origin, status, title, raw, html, plain, excerpt, language, hash string
		var published time.Time
		var offlineReason *string
		if err := rows.Scan(&id, &origin, &status, &published, &currentRevision, &lockVersion,
			&offlineReason, &revision, &title, &raw, &html, &plain, &excerpt, &language, &hash); err != nil {
			t.Fatal(err)
		}
		if origin != "rss" || revision != 1 || currentRevision <= 0 || lockVersion != 1 {
			t.Fatalf("文章 %d 的身份/修订/锁错误", id)
		}
		if id == 41 && (status != "published" || title != "公开历史" || raw != "<p>raw-public</p>" || hash != strings.Repeat("b", 64)) {
			t.Fatalf("公开历史文章回填错误: %d/%s/%s/%s/%s", id, status, title, raw, hash)
		}
		if id == 73 && (status != "offline" || offlineReason == nil || *offlineReason != "admin" || title != "隐藏历史" || hash != strings.Repeat("d", 64)) {
			t.Fatalf("隐藏历史文章回填错误: %d/%s/%v/%s/%s", id, status, offlineReason, title, hash)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("回填文章数 = %d，期望 2", count)
	}
}

func seedI2User(t *testing.T, env *testEnv) {
	t.Helper()
	_, err := env.pool.Exec(context.Background(), `BEGIN;
SET CONSTRAINTS ALL DEFERRED;
INSERT INTO velis.users (id, username, nickname, password_hash, role, status, created_at, updated_at)
VALUES ('10000000-0000-0000-0000-000000000001', 'migration_user', '迁移用户', 'hash', 'user', 'active', now(), now());
INSERT INTO velis.articles (id, origin_type, author_user_id, status, discovered_at, published_at,
current_revision_id, last_seen_at, created_at, updated_at)
OVERRIDING SYSTEM VALUE VALUES (1001, 'user', '10000000-0000-0000-0000-000000000001',
'draft', now(), NULL, 9000001, now(), now(), now());
INSERT INTO velis.article_versions (id, article_id, revision_no, title, user_markdown,
sanitized_html, plain_text, excerpt, language, content_hash, sanitizer_version,
created_by_user_id, created_at) OVERRIDING SYSTEM VALUE VALUES
(9000001, 1001, 1, '', '', '', '', '', 'und', repeat('1', 64), 1,
'10000000-0000-0000-0000-000000000001', now());
COMMIT;`)
	if err != nil {
		t.Fatal(err)
	}
}

func seedAsset(t *testing.T, env *testEnv) {
	t.Helper()
	_, err := env.pool.Exec(context.Background(), `INSERT INTO velis.users
(id, username, nickname, password_hash, role, status, created_at, updated_at)
VALUES ('20000000-0000-0000-0000-000000000002', 'asset_user', '资产用户', 'hash', 'user', 'active', now(), now());
INSERT INTO velis.article_assets (id, owner_user_id, object_key, status, created_at, updated_at)
VALUES ('20000000-0000-0000-0000-000000000003', '20000000-0000-0000-0000-000000000002',
'article-assets/owner/asset/original', 'pending', now(), now())`)
	if err != nil {
		t.Fatal(err)
	}
}

func assertGuardRejects(t *testing.T, env *testEnv, downSQL []byte, reason string) {
	t.Helper()
	if _, err := env.pool.Exec(context.Background(), string(downSQL)); err == nil || !strings.Contains(err.Error(), reason) {
		t.Fatalf("down guard 应因%s拒绝，实际错误: %v", reason, err)
	}
	var exists bool
	if err := env.pool.QueryRow(context.Background(), `SELECT to_regclass('velis.article_versions') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Fatalf("down guard 失败后结构必须原子保留: exists=%t err=%v", exists, err)
	}
}

func newMigrationRunner(t *testing.T, databaseURL string) *migrate.Migrate {
	t.Helper()
	abs, err := filepath.Abs("../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", "public")
	parsed.RawQuery = query.Encode()
	runner, err := migrate.New("file://"+filepath.ToSlash(abs), parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sourceErr, databaseErr := runner.Close(); sourceErr != nil && !errors.Is(sourceErr, os.ErrClosed) {
			t.Logf("关闭迁移 source: %v", sourceErr)
		} else if databaseErr != nil && !errors.Is(databaseErr, os.ErrClosed) {
			t.Logf("关闭迁移 database: %v", databaseErr)
		}
	})
	return runner
}
