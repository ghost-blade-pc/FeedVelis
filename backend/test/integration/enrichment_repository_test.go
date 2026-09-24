package integration

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestEnrichmentRepositoryLeaseFencingAndStageCommits(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	repository := postgres.NewEnrichmentRepository(env.pool)
	now := time.Now().UTC()
	gen := &enrichment.ActiveProfile{Provider: "stub", Model: "chat", ProfileVersion: "g-v1", WorkflowVersion: "w-v1", PromptVersion: "p-v1", StructuredOutput: "prompt", MaxAttempts: 3, AuditTokenBudget: 100, StageBudget: time.Minute}
	embed := &enrichment.ActiveProfile{Provider: "stub", Model: "embed", ProfileVersion: "e-v1", InputVersion: "i-v1", Dimensions: 2, MaxAttempts: 3, AuditTokenBudget: 100, StageBudget: time.Minute}

	var claims [2]*enrichment.ClaimedTask
	var errs [2]error
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range claims {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			claims[index], errs[index] = repository.Claim(context.Background(), enrichment.ClaimRequest{Owner: "worker-" + string(rune('a'+index)), Lease: time.Minute, Generation: gen, Embedding: embed, Now: now})
		}(index)
	}
	close(start)
	wait.Wait()
	var task *enrichment.ClaimedTask
	claimed := 0
	for index := range claims {
		if errs[index] != nil {
			t.Fatal(errs[index])
		}
		if claims[index] != nil {
			claimed++
			task = claims[index]
		}
	}
	if claimed != 1 || task.Stage != "generation" || task.Attempt != 1 || task.LeaseToken == "" {
		t.Fatalf("并发认领结果: %+v %+v", claims[0], claims[1])
	}
	// 认领事务已提交；模型调用期间其他事务可以立即锁定任务行。
	probeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	probe, err := env.pool.Begin(probeCtx)
	if err != nil {
		t.Fatal(err)
	}
	var probedID string
	if err := probe.QueryRow(probeCtx, `SELECT id::text FROM velis.async_tasks WHERE id=$1 FOR UPDATE`, task.ID).Scan(&probedID); err != nil {
		_ = probe.Rollback(probeCtx)
		t.Fatalf("认领后仍持有长事务锁: %v", err)
	}
	if err := probe.Rollback(probeCtx); err != nil {
		t.Fatal(err)
	}

	call := enrichment.CallRecord{Kind: "generation_single", InputHash: strings.Repeat("c", 64), Status: "succeeded", Duration: time.Millisecond}
	if err := repository.RecordCalls(context.Background(), *task, *gen, []enrichment.CallRecord{call}, now); err != nil {
		t.Fatal(err)
	}
	invalidCall := enrichment.CallRecord{Kind: "generation_reduce", InputHash: strings.Repeat("e", 64), Status: "failed", ErrorCode: enrichment.ErrorInvalidOutput,
		ErrorBrief: "模型输出未通过严格校验", ErrorReason: enrichment.ReasonUnknownField, Duration: time.Millisecond}
	if err := repository.RecordCalls(context.Background(), *task, *gen, []enrichment.CallRecord{invalidCall}, now); err != nil {
		t.Fatal(err)
	}
	var errorCode, errorMessage, structuredMode, errorReason string
	if err := env.pool.QueryRow(context.Background(), `SELECT error_code,error_message,structured_output_mode,error_reason FROM velis.ai_model_calls
WHERE task_id=$1 AND call_kind='generation_reduce'`, task.ID).Scan(&errorCode, &errorMessage, &structuredMode, &errorReason); err != nil {
		t.Fatal(err)
	}
	if errorCode != string(enrichment.ErrorInvalidOutput) || errorMessage != "模型输出未通过严格校验" || structuredMode != "prompt" || errorReason != enrichment.ReasonUnknownField {
		t.Fatalf("非法输出审计错误: %s/%s/%s/%s", errorCode, errorMessage, structuredMode, errorReason)
	}
	for _, forbidden := range []string{"正文", "Prompt", "原始输出", "secret"} {
		if strings.Contains(errorMessage, forbidden) || strings.Contains(errorReason, forbidden) {
			t.Fatalf("非法输出审计泄露 %q", forbidden)
		}
	}
	result := enrichment.GenerationResult{ID: "72000000-0000-0000-0000-000000000001", ArticleID: task.ArticleID, RevisionID: task.RevisionID, Profile: *gen, InputHash: strings.Repeat("d", 64), Content: enrichment.GeneratedContent{Summary: "摘要", Keywords: []string{"词"}, Topics: []string{"主题"}}, GeneratedAt: now}
	ok, err := repository.SaveGeneration(context.Background(), *task, result, true, now.Add(time.Second))
	if err != nil || !ok {
		t.Fatalf("保存 generation ok=%t err=%v", ok, err)
	}
	// 同一 generation 的旧 token 已因阶段推进而失效。
	if ok, err = repository.SaveGeneration(context.Background(), *task, result, true, now.Add(2*time.Second)); err != nil || ok {
		t.Fatalf("迟到 generation 应被 fencing: ok=%t err=%v", ok, err)
	}

	embedTask, err := repository.Claim(context.Background(), enrichment.ClaimRequest{Owner: "worker-c", Lease: time.Minute, Generation: gen, Embedding: embed, Now: now.Add(2 * time.Second)})
	if err != nil || embedTask == nil || embedTask.Stage != "embedding" {
		t.Fatalf("认领 embedding: %+v err=%v", embedTask, err)
	}
	current, err := repository.CurrentGeneration(context.Background(), *embedTask)
	if err != nil || current == nil || current.Content.Summary != "摘要" {
		t.Fatalf("当前 generation=%+v err=%v", current, err)
	}
	embedding := enrichment.EmbeddingResult{ID: "72000000-0000-0000-0000-000000000002", GenerationResultID: current.ID, ArticleID: embedTask.ArticleID, RevisionID: embedTask.RevisionID, Profile: *embed, InputHash: strings.Repeat("e", 64), Vector: []float64{0.25, 0.75}, GeneratedAt: now}
	ok, err = repository.SaveEmbedding(context.Background(), *embedTask, embedding, now.Add(3*time.Second))
	if err != nil || !ok {
		t.Fatalf("保存 embedding ok=%t err=%v", ok, err)
	}
	var status, stage string
	var generationID, embeddingID string
	err = env.pool.QueryRow(context.Background(), `SELECT t.status,t.stage,s.generation_result_id::text,s.embedding_result_id::text FROM velis.async_tasks t JOIN velis.ai_current_selections s ON s.article_id=t.article_id WHERE t.id=$1`, task.ID).Scan(&status, &stage, &generationID, &embeddingID)
	if err != nil || status != "succeeded" || stage != "embedding" || generationID != result.ID || embeddingID != embedding.ID {
		t.Fatalf("终态=%s/%s pointers=%s/%s err=%v", status, stage, generationID, embeddingID, err)
	}
}

func TestEnrichmentRepositoryKeepsOldVectorUntilReplacementSucceeds(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	ctx := context.Background()
	now := time.Now().UTC()
	_, err := env.pool.Exec(ctx, `INSERT INTO velis.ai_generation_results(id,article_id,revision_id,provider,model,profile_version,workflow_version,prompt_version,generation_input_hash,input_truncated,summary,keywords,topics,generated_at)
VALUES('75000000-0000-0000-0000-000000000001',7101,7111,'stub','old-chat','g-old','w1','p1',repeat('a',64),false,'旧摘要',ARRAY['旧词'],ARRAY['旧主题'],$1)`, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.ai_embedding_results(id,article_id,revision_id,generation_result_id,provider,model,profile_version,embedding_input_version,embedding_input_hash,dimensions,vector,generated_at)
VALUES('75000000-0000-0000-0000-000000000002',7101,7111,'75000000-0000-0000-0000-000000000001','stub','old-embed','e-old','i1',repeat('b',64),2,ARRAY[0.1,0.2]::real[],$1)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.ai_current_selections(article_id,revision_id,generation_result_id,embedding_result_id,generation_profile_version,embedding_profile_version,updated_at)
VALUES(7101,7111,'75000000-0000-0000-0000-000000000001','75000000-0000-0000-0000-000000000002','g-old','e-old',$1)`, now); err != nil {
		t.Fatal(err)
	}
	repository := postgres.NewEnrichmentRepository(env.pool)
	gen := &enrichment.ActiveProfile{Provider: "stub", Model: "new-chat", ProfileVersion: "g-new", WorkflowVersion: "w2", PromptVersion: "p2", StructuredOutput: "prompt", MaxAttempts: 2}
	embed := &enrichment.ActiveProfile{Provider: "stub", Model: "new-embed", ProfileVersion: "e-new", InputVersion: "i2", Dimensions: 2, MaxAttempts: 2}
	task, err := repository.Claim(ctx, enrichment.ClaimRequest{Owner: "worker", Lease: time.Minute, Generation: gen, Embedding: embed, Now: now})
	if err != nil || task == nil {
		t.Fatalf("claim=%+v err=%v", task, err)
	}
	newGeneration := enrichment.GenerationResult{ID: "75000000-0000-0000-0000-000000000003", ArticleID: 7101, RevisionID: 7111, Profile: *gen, InputHash: strings.Repeat("c", 64), Content: enrichment.GeneratedContent{Summary: "新摘要", Keywords: []string{"新词"}, Topics: []string{"新主题"}}, GeneratedAt: now}
	if saved, err := repository.SaveGeneration(ctx, *task, newGeneration, true, now); err != nil || !saved {
		t.Fatalf("保存新 generation saved=%t err=%v", saved, err)
	}
	var generationID, embeddingID string
	if err := env.pool.QueryRow(ctx, `SELECT generation_result_id::text,embedding_result_id::text FROM velis.ai_current_selections WHERE article_id=7101`).Scan(&generationID, &embeddingID); err != nil || generationID != newGeneration.ID || embeddingID != "75000000-0000-0000-0000-000000000002" {
		t.Fatalf("新 generation 完成时旧向量未保留 generation=%s embedding=%s err=%v", generationID, embeddingID, err)
	}
	embedTask, err := repository.Claim(ctx, enrichment.ClaimRequest{Owner: "worker", Lease: time.Minute, Generation: gen, Embedding: embed, Now: now.Add(time.Second)})
	if err != nil || embedTask == nil {
		t.Fatalf("embedding claim=%+v err=%v", embedTask, err)
	}
	newEmbedding := enrichment.EmbeddingResult{ID: "75000000-0000-0000-0000-000000000004", GenerationResultID: newGeneration.ID, ArticleID: 7101, RevisionID: 7111, Profile: *embed, InputHash: strings.Repeat("d", 64), Vector: []float64{0.3, 0.4}, GeneratedAt: now}
	if saved, err := repository.SaveEmbedding(ctx, *embedTask, newEmbedding, now.Add(time.Second)); err != nil || !saved {
		t.Fatalf("保存新 embedding saved=%t err=%v", saved, err)
	}
	if err := env.pool.QueryRow(ctx, `SELECT embedding_result_id::text FROM velis.ai_current_selections WHERE article_id=7101`).Scan(&embeddingID); err != nil || embeddingID != newEmbedding.ID {
		t.Fatalf("新向量未切换 embedding=%s err=%v", embeddingID, err)
	}
}

func TestEnrichmentRepositoryRejectsExpiredAndChangedGeneration(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	repository := postgres.NewEnrichmentRepository(env.pool)
	now := time.Now().UTC()
	gen := &enrichment.ActiveProfile{Provider: "stub", Model: "chat", ProfileVersion: "g-v1", WorkflowVersion: "w-v1", PromptVersion: "p-v1", StructuredOutput: "prompt", MaxAttempts: 3}
	task, err := repository.Claim(context.Background(), enrichment.ClaimRequest{Owner: "worker", Lease: time.Second, Generation: gen, Now: now})
	if err != nil || task == nil {
		t.Fatalf("claim=%+v err=%v", task, err)
	}
	result := enrichment.GenerationResult{ID: "72000000-0000-0000-0000-000000000011", ArticleID: task.ArticleID, RevisionID: task.RevisionID, Profile: *gen, InputHash: strings.Repeat("f", 64), Content: enrichment.GeneratedContent{Summary: "摘要", Keywords: []string{"词"}, Topics: []string{"主题"}}, GeneratedAt: now}
	if ok, err := repository.SaveGeneration(context.Background(), *task, result, false, now.Add(2*time.Second)); err != nil || ok {
		t.Fatalf("过期租约应拒绝: ok=%t err=%v", ok, err)
	}
	if _, err := env.pool.Exec(context.Background(), `UPDATE velis.async_tasks SET status='pending',generation=generation+1,lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,next_attempt_at=$2 WHERE id=$1`, task.ID, now); err != nil {
		t.Fatal(err)
	}
	if ok, err := repository.SaveGeneration(context.Background(), *task, result, false, now); err != nil || ok {
		t.Fatalf("旧 generation 应拒绝: ok=%t err=%v", ok, err)
	}
	var count int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM velis.ai_generation_results WHERE id=$1`, result.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale 结果被保存 count=%d err=%v", count, err)
	}
}

func TestEnrichmentRepositoryRepairReservationIsAtomicAndSurvivesLeaseRecovery(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	ctx := context.Background()
	repository := postgres.NewEnrichmentRepository(env.pool)
	now := time.Now().UTC()
	gen := &enrichment.ActiveProfile{Provider: "stub", Model: "chat", ProfileVersion: "g-v1", WorkflowVersion: "w-v1", PromptVersion: "p-v1", StructuredOutput: "prompt", MaxAttempts: 3}
	task, err := repository.Claim(ctx, enrichment.ClaimRequest{Owner: "worker-a", Lease: time.Second, Generation: gen, Now: now})
	if err != nil || task == nil || task.GenerationRepairUsed {
		t.Fatalf("首次 claim=%+v err=%v", task, err)
	}

	start := make(chan struct{})
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			reserved, reserveErr := repository.ReserveGenerationRepair(ctx, *task, now.Add(100*time.Millisecond))
			results <- reserved
			errs <- reserveErr
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errs)
	reservedCount := 0
	for reserveErr := range errs {
		if reserveErr != nil {
			t.Fatal(reserveErr)
		}
	}
	for reserved := range results {
		if reserved {
			reservedCount++
		}
	}
	if reservedCount != 1 {
		t.Fatalf("并发预占成功数=%d", reservedCount)
	}

	recovered, err := repository.Claim(ctx, enrichment.ClaimRequest{Owner: "worker-b", Lease: time.Minute, Generation: gen, Now: now.Add(2 * time.Second)})
	if err != nil || recovered == nil || !recovered.GenerationRepairUsed || recovered.Attempt != 2 {
		t.Fatalf("租约恢复未保留纠正额度: task=%+v err=%v", recovered, err)
	}
	if reserved, err := repository.ReserveGenerationRepair(ctx, *recovered, now.Add(3*time.Second)); err != nil || reserved {
		t.Fatalf("恢复后不应再次预占: reserved=%t err=%v", reserved, err)
	}
}

func TestEnrichmentRepositoryFencesChangedArticleAndProfile(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(context.Context, *testEnv, enrichment.ClaimedTask) error
	}{
		{"编辑", func(ctx context.Context, env *testEnv, task enrichment.ClaimedTask) error {
			if _, err := env.pool.Exec(ctx, `INSERT INTO velis.article_versions(id,article_id,revision_no,title,raw_description,raw_content,sanitized_html,plain_text,excerpt,language,content_hash,sanitizer_version,created_at)
OVERRIDING SYSTEM VALUE VALUES(7113,$1,2,'新标题','','新正文','<p>新正文</p>','新正文','新正文','zh-CN',repeat('c',64),1,now())`, task.ArticleID); err != nil {
				return err
			}
			_, err := env.pool.Exec(ctx, `UPDATE velis.articles SET current_revision_id=7113,lock_version=lock_version+1,updated_at=now() WHERE id=$1`, task.ArticleID)
			return err
		}},
		{"下架", func(ctx context.Context, env *testEnv, task enrichment.ClaimedTask) error {
			_, err := env.pool.Exec(ctx, `UPDATE velis.articles SET status='offline',offline_reason='admin',offline_at=now(),lock_version=lock_version+1,updated_at=now() WHERE id=$1`, task.ArticleID)
			return err
		}},
		{"删除", func(ctx context.Context, env *testEnv, task enrichment.ClaimedTask) error {
			_, err := env.pool.Exec(ctx, `UPDATE velis.articles SET status='deleted',deleted_at=now(),lock_version=lock_version+1,updated_at=now() WHERE id=$1`, task.ArticleID)
			return err
		}},
		{"profile 升级", func(ctx context.Context, env *testEnv, task enrichment.ClaimedTask) error {
			_, err := env.pool.Exec(ctx, `UPDATE velis.async_tasks SET generation_profile_version='g-v2' WHERE id=$1`, task.ID)
			return err
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			env := newTestEnv(t)
			env.resetArticles(t)
			env.resetAccounts(t)
			seedAIUpgradeTasks(t, env)
			ctx := context.Background()
			repository := postgres.NewEnrichmentRepository(env.pool)
			now := time.Now().UTC()
			profile := &enrichment.ActiveProfile{Provider: "stub", Model: "chat", ProfileVersion: "g-v1", WorkflowVersion: "w-v1", PromptVersion: "p-v1", StructuredOutput: "prompt", MaxAttempts: 3}
			task, err := repository.Claim(ctx, enrichment.ClaimRequest{Owner: "worker", Lease: time.Minute, Generation: profile, Now: now})
			if err != nil || task == nil {
				t.Fatalf("claim=%+v err=%v", task, err)
			}
			if err := mutation.mutate(ctx, env, *task); err != nil {
				t.Fatal(err)
			}
			result := enrichment.GenerationResult{ID: "74000000-0000-0000-0000-000000000001", ArticleID: task.ArticleID, RevisionID: task.RevisionID, Profile: *profile, InputHash: strings.Repeat("a", 64), Content: enrichment.GeneratedContent{Summary: "迟到摘要", Keywords: []string{"词"}, Topics: []string{"主题"}}, GeneratedAt: now}
			if saved, err := repository.SaveGeneration(ctx, *task, result, false, now.Add(time.Second)); err != nil || saved {
				t.Fatalf("变化后迟到结果应丢弃 saved=%t err=%v", saved, err)
			}
			failed, err := repository.Fail(ctx, enrichment.FailureUpdate{Task: *task, Code: enrichment.ErrorTimeout, Message: "超时", Retry: true, NextAttemptAt: now.Add(time.Minute), Now: now.Add(time.Second)})
			if err != nil || failed {
				t.Fatalf("变化后迟到失败写回应丢弃 failed=%t err=%v", failed, err)
			}
			var count int
			if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.ai_generation_results WHERE id=$1`, result.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("迟到结果被保存 count=%d err=%v", count, err)
			}
		})
	}
}
