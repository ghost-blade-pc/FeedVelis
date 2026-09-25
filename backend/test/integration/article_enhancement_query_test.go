package integration

import (
	"context"
	"testing"
	"time"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestPublishedQueriesExposeOnlyCurrentRevisionGenerationWithoutVector(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	ctx := context.Background()
	now := time.Now().UTC()
	_, err := env.pool.Exec(ctx, `INSERT INTO velis.ai_generation_results(id,article_id,revision_id,provider,model,profile_version,workflow_version,prompt_version,generation_input_hash,input_truncated,summary,keywords,topics,generated_at)
VALUES('73000000-0000-0000-0000-000000000001',7101,7111,'stub','chat','g-old','w1','p1',repeat('a',64),false,'AI 摘要',ARRAY['关键词'],ARRAY['主题'],$1)`, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.pool.Exec(ctx, `INSERT INTO velis.ai_current_selections(article_id,revision_id,generation_result_id,generation_profile_version,updated_at)
VALUES(7101,7111,'73000000-0000-0000-0000-000000000001','g-old',$1)`, now)
	if err != nil {
		t.Fatal(err)
	}
	repository := postgres.NewArticleRepository(env.pool)
	batch, err := repository.ListPublishedByIDs(ctx, []int64{7102, 7101, 999999})
	if err != nil || len(batch) != 2 {
		t.Fatalf("批量公开读取失败: items=%+v err=%v", batch, err)
	}
	var batchItem *string
	for _, item := range batch {
		if item.ID == 7101 && item.Enhancement != nil {
			value := item.Enhancement.Summary
			batchItem = &value
		}
	}
	if batchItem == nil || *batchItem != "AI 摘要" {
		t.Fatalf("批量读取未连接当前 generation: %+v", batch)
	}
	items, err := repository.ListPublished(ctx, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, item := range items {
		if item.ID == 7101 {
			found = true
			if item.Enhancement == nil || item.Enhancement.Summary != "AI 摘要" || len(item.Enhancement.Keywords) != 1 {
				t.Fatalf("enhancement=%+v", item.Enhancement)
			}
		}
	}
	if !found {
		t.Fatal("未读取测试文章")
	}
	detail, err := repository.GetPublished(ctx, 7101)
	if err != nil || detail.Item.Enhancement == nil || detail.Item.Enhancement.Summary != "AI 摘要" {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	// 新修订切换后旧指针仍留存，但读取必须立即隐藏旧结果，排序字段不变。
	_, err = env.pool.Exec(ctx, `INSERT INTO velis.article_versions(id,article_id,revision_no,title,raw_description,raw_content,sanitized_html,plain_text,excerpt,language,content_hash,sanitizer_version,created_at) OVERRIDING SYSTEM VALUE
VALUES(7113,7101,2,'新标题','','新正文','<p>新正文</p>','新正文','新正文','zh-CN',repeat('c',64),1,$1)`, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.pool.Exec(ctx, `UPDATE velis.articles SET current_revision_id=7113,lock_version=lock_version+1,updated_at=$1 WHERE id=7101`, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	detail, err = repository.GetPublished(ctx, 7101)
	if err != nil || detail.Item.Enhancement != nil || detail.Item.Excerpt != "新正文" {
		t.Fatalf("旧修订泄露: %+v err=%v", detail.Item, err)
	}
	batch, err = repository.ListPublishedByIDs(ctx, []int64{7101})
	if err != nil || len(batch) != 1 || batch[0].Enhancement != nil || batch[0].Excerpt != "新正文" {
		t.Fatalf("批量读取泄露旧修订 generation: %+v err=%v", batch, err)
	}
	reason := articleDomain.OfflineByAdmin
	if _, err = repository.SetArticleState(ctx, 7101, 2, articleDomain.StatusOffline, &reason, nil, nil, nil, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	batch, err = repository.ListPublishedByIDs(ctx, []int64{7101})
	if err != nil || len(batch) != 0 {
		t.Fatalf("批量读取泄露下架文章: %+v err=%v", batch, err)
	}
}
