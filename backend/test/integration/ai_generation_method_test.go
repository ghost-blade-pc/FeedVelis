package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestGenerationMethodsCoexistAndExtractiveCanBeRequeued(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedFeedbackArticle(t, env)
	ctx := context.Background()
	for _, record := range []struct{ id, method string }{
		{"73000000-0000-0000-0000-000000000001", "model"},
		{"73000000-0000-0000-0000-000000000002", "extractive"},
	} {
		_, err := env.pool.Exec(ctx, `INSERT INTO velis.ai_generation_results
(id,article_id,revision_id,provider,model,profile_version,workflow_version,prompt_version,generation_input_hash,input_truncated,summary,keywords,topics,generated_at,generation_method)
VALUES($1,7201,7211,'stub','chat','g-v1','w-v1','p-v1',repeat('a',64),false,'正文',ARRAY['标题'],ARRAY['标题'],now(),$2)`, record.id, record.method)
		if err != nil {
			t.Fatalf("两种结果应可共存: %v", err)
		}
	}
	_, err := env.pool.Exec(ctx, `INSERT INTO velis.ai_current_selections(article_id,revision_id,generation_result_id,generation_profile_version,updated_at)
VALUES(7201,7211,'73000000-0000-0000-0000-000000000002','g-v1',now())`)
	if err != nil {
		t.Fatal(err)
	}
	article, err := postgres.NewArticleRepository(env.pool).GetPublished(ctx, 7201)
	if err != nil || article.Item.Enhancement == nil || article.Item.Enhancement.Method != "extractive" {
		t.Fatalf("文章读取未保留摘录来源: article=%+v err=%v", article, err)
	}
	request := enrichment.BackfillRequest{Stage: "generation", Mode: "outdated-only", ArticleID: 7201, GenerationProfile: "g-v1"}
	repository := postgres.NewEnrichmentBackfillRepository(env.pool)
	candidates, _, err := repository.Candidates(ctx, request, nil, 1, 0)
	if err != nil || len(candidates) != 1 || candidates[0].ArticleID != 7201 {
		t.Fatalf("相同 profile 的摘录结果应能预览重算: candidates=%+v err=%v", candidates, err)
	}
	preview := request
	preview.DryRun = true
	report, err := enrichment.NewAIBackfillService(repository).Run(ctx, preview)
	if err != nil || report.Created != 1 {
		t.Fatalf("只读预览应报告一篇候选: report=%+v err=%v", report, err)
	}
	var taskCount int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.async_tasks WHERE article_id=7201`).Scan(&taskCount); err != nil || taskCount != 0 {
		t.Fatalf("dry-run 不应创建模型任务: count=%d err=%v", taskCount, err)
	}
	if advanced, err := repository.AdvanceCandidate(ctx, 7201, request); err != nil || !advanced {
		t.Fatalf("摘录结果未被显式重排: advanced=%t err=%v", advanced, err)
	}
	_, err = env.pool.Exec(ctx, `UPDATE velis.ai_current_selections
SET generation_result_id='73000000-0000-0000-0000-000000000001' WHERE article_id=7201`)
	if err != nil {
		t.Fatal(err)
	}
	article, err = postgres.NewArticleRepository(env.pool).GetPublished(ctx, 7201)
	if err != nil || article.Item.Enhancement == nil || article.Item.Enhancement.Method != "model" {
		t.Fatalf("模型结果未能替换摘录: article=%+v err=%v", article, err)
	}
}

func TestGenerationMethodRollbackGuardPreservesExtractiveRows(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedFeedbackArticle(t, env)
	ctx := context.Background()
	_, err := env.pool.Exec(ctx, `INSERT INTO velis.ai_generation_results
(id,article_id,revision_id,provider,model,profile_version,workflow_version,prompt_version,generation_input_hash,input_truncated,summary,keywords,topics,generated_at,generation_method)
VALUES('73000000-0000-0000-0000-000000000003',7201,7211,'stub','chat','g-v1','w-v1','p-v1',repeat('b',64),false,'正文',ARRAY['标题'],ARRAY['标题'],now(),'extractive')`)
	if err != nil {
		t.Fatal(err)
	}
	downSQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000011_add_generation_method.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, string(downSQL)); err == nil || !strings.Contains(err.Error(), "存在摘录降级结果") {
		t.Fatalf("含摘录结果的降级迁移应拒绝: %v", err)
	}
	var count int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.ai_generation_results WHERE generation_method='extractive'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("失败回滚应保留摘录事实: count=%d err=%v", count, err)
	}
}
