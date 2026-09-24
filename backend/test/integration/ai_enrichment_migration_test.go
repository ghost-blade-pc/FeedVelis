package integration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
)

func TestAIEnrichmentMigrationUpgradeConstraintsAndSafeDown(t *testing.T) {
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
		t.Fatalf("退回 v6: %v", err)
	}
	seedAIUpgradeTasks(t, env)
	if err := runner.Steps(1); err != nil {
		t.Fatalf("升级 v7: %v", err)
	}

	rows, err := env.pool.Query(ctx, `SELECT status,stage,generation_attempt,embedding_attempt,lease_token IS NULL
FROM velis.async_tasks ORDER BY article_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := []string{"pending", "canceled"}
	for index := 0; rows.Next(); index++ {
		var status, stage string
		var generationAttempt, embeddingAttempt int
		var leaseEmpty bool
		if err := rows.Scan(&status, &stage, &generationAttempt, &embeddingAttempt, &leaseEmpty); err != nil {
			t.Fatal(err)
		}
		if index >= len(want) || status != want[index] || stage != "generation" || generationAttempt != 0 || embeddingAttempt != 0 || !leaseEmpty {
			t.Fatalf("升级后的任务状态错误: %s/%s/%d/%d/%t", status, stage, generationAttempt, embeddingAttempt, leaseEmpty)
		}
	}

	const resultID = "71000000-0000-0000-0000-000000000021"
	_, err = env.pool.Exec(ctx, `INSERT INTO velis.ai_generation_results
(id,article_id,revision_id,provider,model,profile_version,workflow_version,prompt_version,generation_input_hash,input_truncated,summary,keywords,topics,generated_at)
VALUES($1,7101,7111,'stub','chat','g-v1','w-v1','p-v1',repeat('a',64),false,'摘要',ARRAY['关键词'],ARRAY['主题'],now())`, resultID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = env.pool.Exec(ctx, `INSERT INTO velis.ai_generation_results
(id,article_id,revision_id,provider,model,profile_version,workflow_version,prompt_version,generation_input_hash,input_truncated,summary,keywords,topics,generated_at)
VALUES('71000000-0000-0000-0000-000000000022',7101,7111,'stub','chat','g-v1','w-v1','p-v1',repeat('a',64),false,'重复',ARRAY['关键词'],ARRAY['主题'],now())`); err == nil {
		t.Fatal("相同 generation 目标必须幂等冲突")
	}
	if _, err = env.pool.Exec(ctx, `INSERT INTO velis.ai_embedding_results
(id,article_id,revision_id,generation_result_id,provider,model,profile_version,embedding_input_version,embedding_input_hash,dimensions,vector,generated_at)
VALUES('71000000-0000-0000-0000-000000000023',7101,7111,$1,'stub','embed','e-v1','i-v1',repeat('b',64),3,ARRAY[1,2]::real[],now())`, resultID); err == nil {
		t.Fatal("向量维度不符必须被数据库约束拒绝")
	}

	if err := runner.Steps(-1); err == nil || !strings.Contains(err.Error(), "拒绝回滚 AI 内容增强迁移") {
		t.Fatalf("存在结果时 down 应拒绝，实际: %v", err)
	}
	if err := runner.Force(7); err != nil {
		t.Fatalf("恢复失败迁移版本: %v", err)
	}
	if _, err := env.pool.Exec(ctx, `DELETE FROM velis.ai_generation_results; DELETE FROM velis.async_tasks;
DELETE FROM velis.article_versions; DELETE FROM velis.articles; DELETE FROM velis.users WHERE username='ai_migration_user'`); err != nil {
		t.Fatal(err)
	}
	if err := runner.Steps(-1); err != nil {
		t.Fatalf("空增强数据 down: %v", err)
	}
}

func seedAIUpgradeTasks(t *testing.T, env *testEnv) {
	t.Helper()
	_, err := env.pool.Exec(context.Background(), `
SET CONSTRAINTS ALL DEFERRED;
INSERT INTO velis.users(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES('71000000-0000-0000-0000-000000000001','ai_migration_user','迁移用户','hash','user','active',now(),now());
INSERT INTO velis.articles(id,origin_type,author_user_id,status,current_revision_id,lock_version,published_at,discovered_at,last_seen_at,created_at,updated_at)
OVERRIDING SYSTEM VALUE
VALUES (7101,'user','71000000-0000-0000-0000-000000000001','published',7111,1,now(),now(),now(),now(),now()),
       (7102,'user','71000000-0000-0000-0000-000000000001','published',7112,1,now(),now(),now(),now(),now());
INSERT INTO velis.article_versions(id,article_id,revision_no,title,raw_description,raw_content,sanitized_html,plain_text,excerpt,language,content_hash,sanitizer_version,created_at)
OVERRIDING SYSTEM VALUE
VALUES (7111,7101,1,'公开','','正文','<p>正文</p>','正文','正文','zh-CN',repeat('a',64),1,now()),
       (7112,7102,1,'离线','','正文','<p>正文</p>','正文','正文','zh-CN',repeat('b',64),1,now());
INSERT INTO velis.async_tasks(id,task_type,aggregate_type,aggregate_id,article_id,revision_id,revision_no,content_hash,status,generation,observed_aggregate_version,created_at,updated_at,canceled_at)
VALUES ('71000000-0000-0000-0000-000000000011','article.enrichment','article','7101',7101,7111,1,repeat('a',64),'pending',1,1,now(),now(),NULL),
       ('71000000-0000-0000-0000-000000000012','article.enrichment','article','7102',7102,7112,1,repeat('b',64),'canceled',2,1,now(),now(),now());`)
	if err != nil {
		t.Fatal(err)
	}
}
