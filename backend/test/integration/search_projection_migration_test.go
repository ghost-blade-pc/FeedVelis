package integration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
)

func TestSearchProjectionMigrationUpgradeConstraintsAndSafeDown(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	runner := newMigrationRunner(t, env.databaseURL)
	ctx := context.Background()
	defer func() {
		if err := runner.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			t.Errorf("清理时恢复最新迁移: %v", err)
		}
	}()

	if err := runner.Steps(-1); err != nil {
		t.Fatalf("退回 v8: %v", err)
	}
	seedSearchProjectionArticles(t, env)
	if err := runner.Steps(1); err != nil {
		t.Fatalf("升级 v9: %v", err)
	}

	var sequenceExists bool
	if err := env.pool.QueryRow(ctx, `SELECT to_regclass('velis.search_projection_change_seq') IS NOT NULL`).Scan(&sequenceExists); err != nil || !sequenceExists {
		t.Fatalf("新增迁移未创建 change sequence: exists=%t err=%v", sequenceExists, err)
	}

	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.search_index_state
(read_alias,write_alias,current_index,schema_version,schema_identity,updated_at)
VALUES('velis-articles-read','velis-articles-write','velis-articles-v1-20260919t120000z-aaaaaa',1,'mapping-v1|cjk-v1|dim-3|encoding-v1',now())`); err != nil {
		t.Fatalf("写入索引服务状态: %v", err)
	}
	insertSearchProjectionJob(t, env, "upsert", 7201)

	// delivery 只允许指向当前服务索引、回滚索引或活动重建候选索引。
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.search_projection_deliveries
(article_id,physical_index,required_generation,status) VALUES(7201,'velis-articles-v1-20260919t120000z-zzzzzz',1,'pending')`); err == nil {
		t.Fatal("未服务索引的 delivery 必须被拒绝")
	}
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.search_projection_deliveries
(article_id,physical_index,required_generation,status) VALUES(7201,'velis-articles-v1-20260919t120000z-aaaaaa',1,'pending')`); err != nil {
		t.Fatalf("当前服务索引的 delivery 应可写入: %v", err)
	}

	// tombstone 不保留 AI 结果引用。
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.search_projection_jobs
(article_id,action,generation,change_seq,article_lock_version,revision_id,generation_result_id,status,target_changed_at)
VALUES(7202,'tombstone',1,nextval('velis.search_projection_change_seq'),1,7212,'72000000-0000-0000-0000-000000000099','pending',now())`); err == nil {
		t.Fatal("tombstone 携带 generation 引用必须被拒绝")
	}
	// running 槽位必须持有完整租约。
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.search_projection_jobs
(article_id,action,generation,change_seq,article_lock_version,revision_id,status,target_changed_at)
VALUES(7202,'tombstone',1,nextval('velis.search_projection_change_seq'),1,7212,'running',now())`); err == nil {
		t.Fatal("running 槽位缺少租约必须被拒绝")
	}

	insertSearchProjectionJob(t, env, "tombstone", 7202)
	// 同一时刻只允许一个活动重建。
	insertSearchIndexRebuild(t, env, "velis-articles-v2-20260919t130000z-bbbbbb", "snapshot")
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.search_index_rebuilds
(id,candidate_index,target_schema_version,schema_identity,phase,start_change_seq,started_at,updated_at)
VALUES('72000000-0000-0000-0000-0000000000aa','velis-articles-v2-20260919t130000z-cccccc',2,'mapping-v2|cjk-v1|dim-3|encoding-v1','catchup',0,now(),now())`); err == nil {
		t.Fatal("第二个活动重建必须被拒绝")
	}
	// 活动重建候选索引可注册为第二 delivery。
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.search_projection_deliveries
(article_id,physical_index,required_generation,status) VALUES(7201,'velis-articles-v2-20260919t130000z-bbbbbb',1,'pending')`); err != nil {
		t.Fatalf("活动重建候选索引的 delivery 应可写入: %v", err)
	}

	// 存在投影槽位时受保护降级必须拒绝。
	if err := runner.Steps(-1); err == nil || !strings.Contains(err.Error(), "拒绝回滚搜索投影迁移") {
		t.Fatalf("存在投影数据时 down 应拒绝，实际: %v", err)
	}
	if err := runner.Force(9); err != nil {
		t.Fatalf("恢复失败迁移版本: %v", err)
	}

	// 回滚窗口打开时同样拒绝。
	if _, err := env.pool.Exec(ctx, `DELETE FROM velis.search_projection_deliveries;
DELETE FROM velis.search_projection_jobs; DELETE FROM velis.search_index_rebuilds;
UPDATE velis.search_index_state SET rollback_index='velis-articles-v0-20260919t110000z-dddddd',rollback_deadline=now()+interval '1h'`); err != nil {
		t.Fatal(err)
	}
	if err := runner.Steps(-1); err == nil || !strings.Contains(err.Error(), "回滚窗口仍打开") {
		t.Fatalf("回滚窗口打开时 down 应拒绝，实际: %v", err)
	}
	if err := runner.Force(9); err != nil {
		t.Fatalf("恢复失败迁移版本: %v", err)
	}

	if _, err := env.pool.Exec(ctx, `UPDATE velis.search_index_state SET rollback_index=NULL,rollback_deadline=NULL`); err != nil {
		t.Fatal(err)
	}
	if err := runner.Steps(-1); err != nil {
		t.Fatalf("空投影数据 down: %v", err)
	}
}

func seedSearchProjectionArticles(t *testing.T, env *testEnv) {
	t.Helper()
	_, err := env.pool.Exec(context.Background(), `
SET CONSTRAINTS ALL DEFERRED;
INSERT INTO velis.users(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES('72000000-0000-0000-0000-000000000001','search_migration_user','迁移用户','hash','user','active',now(),now());
INSERT INTO velis.articles(id,origin_type,author_user_id,status,current_revision_id,lock_version,published_at,discovered_at,last_seen_at,created_at,updated_at)
OVERRIDING SYSTEM VALUE
VALUES (7201,'user','72000000-0000-0000-0000-000000000001','published',7211,1,now(),now(),now(),now(),now()),
       (7202,'user','72000000-0000-0000-0000-000000000001','draft',7212,1,NULL,now(),now(),now(),now());
INSERT INTO velis.article_versions(id,article_id,revision_no,title,raw_description,raw_content,sanitized_html,plain_text,excerpt,language,content_hash,sanitizer_version,created_at)
OVERRIDING SYSTEM VALUE
VALUES (7211,7201,1,'公开','','正文','<p>正文</p>','正文','正文','zh-CN',repeat('a',64),1,now()),
       (7212,7202,1,'草稿','','正文','<p>正文</p>','正文','正文','zh-CN',repeat('b',64),1,now());`)
	if err != nil {
		t.Fatal(err)
	}
}

func insertSearchProjectionJob(t *testing.T, env *testEnv, action string, articleID int64) {
	t.Helper()
	revisionID := articleID + 10
	_, err := env.pool.Exec(context.Background(), `INSERT INTO velis.search_projection_jobs
(article_id,action,generation,change_seq,article_lock_version,revision_id,status,target_changed_at)
VALUES($1,$2,1,nextval('velis.search_projection_change_seq'),1,$3,'pending',now())`, articleID, action, revisionID)
	if err != nil {
		t.Fatalf("写入投影槽位: %v", err)
	}
}

func insertSearchIndexRebuild(t *testing.T, env *testEnv, candidate, phase string) {
	t.Helper()
	_, err := env.pool.Exec(context.Background(), `INSERT INTO velis.search_index_rebuilds
(id,candidate_index,target_schema_version,schema_identity,phase,start_change_seq,started_at,updated_at)
VALUES('72000000-0000-0000-0000-0000000000bb',$1,2,'mapping-v2|cjk-v1|dim-3|encoding-v1',$2,0,now(),now())`, candidate, phase)
	if err != nil {
		t.Fatalf("写入重建记录: %v", err)
	}
}
