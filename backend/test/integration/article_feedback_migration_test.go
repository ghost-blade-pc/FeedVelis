package integration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
)

func TestArticleFeedbackMigration(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	runner := newMigrationRunner(t, env.databaseURL)
	defer func() {
		if err := runner.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			t.Errorf("恢复最新迁移: %v", err)
		}
	}()

	if err := runner.Migrate(9); err != nil {
		t.Fatalf("空表降级: %v", err)
	}
	if err := runner.Migrate(10); err != nil {
		t.Fatalf("重新升级: %v", err)
	}
	for _, name := range []string{"article_read_windows_pkey", "article_read_windows_cleanup_idx", "article_favorites_pkey", "article_not_interested_pkey", "article_not_interested_cleanup_idx"} {
		var exists bool
		if err := env.pool.QueryRow(context.Background(), `SELECT to_regclass('velis.' || $1) IS NOT NULL`, name).Scan(&exists); err != nil || !exists {
			t.Fatalf("缺少约束或索引 %s: exists=%v err=%v", name, exists, err)
		}
	}
	seedFeedbackArticle(t, env)
	_, err := env.pool.Exec(context.Background(), `INSERT INTO velis.article_favorites(user_id, article_id, created_at)
VALUES ('72000000-0000-0000-0000-000000000001', 7201, now())`)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Migrate(9); err == nil || !strings.Contains(err.Error(), "拒绝删除非空反馈表") {
		t.Fatalf("非空表降级应拒绝: %v", err)
	}
	var count int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM velis.article_favorites`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("降级失败后反馈事实必须保留: count=%d err=%v", count, err)
	}
	if err := runner.Force(10); err != nil {
		t.Fatal(err)
	}
	_, err = env.pool.Exec(context.Background(), `DELETE FROM velis.article_favorites;
DELETE FROM velis.article_versions;
DELETE FROM velis.articles WHERE id=7201;
DELETE FROM velis.users WHERE id='72000000-0000-0000-0000-000000000001'`)
	if err != nil {
		t.Fatal(err)
	}
}

func seedFeedbackArticle(t *testing.T, env *testEnv) {
	t.Helper()
	_, err := env.pool.Exec(context.Background(), `
SET CONSTRAINTS ALL DEFERRED;
INSERT INTO velis.users(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES('72000000-0000-0000-0000-000000000001','feedback_test_user','测试用户','hash','user','active',now(),now());
INSERT INTO velis.articles(id,origin_type,author_user_id,status,current_revision_id,published_at,discovered_at,last_seen_at,created_at,updated_at)
OVERRIDING SYSTEM VALUE
VALUES(7201,'user','72000000-0000-0000-0000-000000000001','published',7211,now(),now(),now(),now(),now());
INSERT INTO velis.article_versions(id,article_id,revision_no,title,plain_text,excerpt,language,content_hash,sanitizer_version,created_at)
OVERRIDING SYSTEM VALUE
VALUES(7211,7201,1,'测试文章','正文','正文','zh-CN',repeat('a',64),1,now());`)
	if err != nil {
		t.Fatal(err)
	}
}
