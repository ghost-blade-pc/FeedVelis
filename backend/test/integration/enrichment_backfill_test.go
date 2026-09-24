package integration

import (
	"context"
	"sync"
	"testing"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestAIBackfillDryRunAndPendingTargetAreIdempotent(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	service := enrichment.NewAIBackfillService(postgres.NewEnrichmentBackfillRepository(env.pool))
	ctx := context.Background()
	request := enrichment.BackfillRequest{Stage: "generation", Mode: "missing-only", Limit: 1, DryRun: true, GenerationProfile: "g-v2"}
	report, err := service.Run(ctx, request)
	if err != nil || report.Created != 1 {
		t.Fatalf("dry report=%+v err=%v", report, err)
	}
	var generation int64
	var profile *string
	if err := env.pool.QueryRow(ctx, `SELECT generation,generation_profile_version FROM velis.async_tasks WHERE article_id=7101`).Scan(&generation, &profile); err != nil || generation != 1 || profile != nil {
		t.Fatalf("dry-run 写入: generation=%d profile=%v err=%v", generation, profile, err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE velis.async_tasks SET generation_repair_used_at=now() WHERE article_id=7101`); err != nil {
		t.Fatal(err)
	}
	request.DryRun = false
	report, err = service.Run(ctx, request)
	if err != nil || report.Created != 1 {
		t.Fatalf("write report=%+v err=%v", report, err)
	}
	if err := env.pool.QueryRow(ctx, `SELECT generation,generation_profile_version FROM velis.async_tasks WHERE article_id=7101`).Scan(&generation, &profile); err != nil || generation != 2 || profile == nil || *profile != "g-v2" {
		t.Fatalf("推进结果 generation=%d profile=%v err=%v", generation, profile, err)
	}
	var repairUsed bool
	if err := env.pool.QueryRow(ctx, `SELECT generation_repair_used_at IS NOT NULL FROM velis.async_tasks WHERE article_id=7101`).Scan(&repairUsed); err != nil || repairUsed {
		t.Fatalf("新 generation 未重置纠正额度: used=%t err=%v", repairUsed, err)
	}
	report, err = service.Run(ctx, request)
	if err != nil || report.Created != 1 || report.Skipped != 0 {
		t.Fatalf("重复运行应跳过已排队目标并继续下一候选: report=%+v err=%v", report, err)
	}
	var after int64
	if err := env.pool.QueryRow(ctx, `SELECT generation FROM velis.async_tasks WHERE article_id=7101`).Scan(&after); err != nil || after != generation {
		t.Fatalf("重复运行递增 generation=%d err=%v", after, err)
	}
	if err := env.pool.QueryRow(ctx, `SELECT generation_profile_version FROM velis.async_tasks WHERE article_id=7102`).Scan(&profile); err != nil || profile == nil || *profile != "g-v2" {
		t.Fatalf("后续候选未推进 profile=%v err=%v", profile, err)
	}
}

func TestAIBackfillConcurrentRunsAdvanceOnce(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	request := enrichment.BackfillRequest{Stage: "generation", Mode: "missing-only", ArticleID: 7101, GenerationProfile: "g-v2"}
	start := make(chan struct{})
	var wait sync.WaitGroup
	reports := make([]enrichment.BackfillReport, 2)
	errs := make([]error, 2)
	for index := range reports {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			reports[index], errs[index] = enrichment.NewAIBackfillService(postgres.NewEnrichmentBackfillRepository(env.pool)).Run(context.Background(), request)
		}(index)
	}
	close(start)
	wait.Wait()
	created, skipped := 0, 0
	for index := range reports {
		if errs[index] != nil {
			t.Fatal(errs[index])
		}
		created += reports[index].Created
		skipped += reports[index].Skipped
	}
	if created != 1 || skipped != 1 {
		t.Fatalf("并发补录 created=%d skipped=%d reports=%+v", created, skipped, reports)
	}
	var generation int64
	if err := env.pool.QueryRow(context.Background(), `SELECT generation FROM velis.async_tasks WHERE article_id=7101`).Scan(&generation); err != nil || generation != 2 {
		t.Fatalf("并发补录 generation=%d err=%v", generation, err)
	}
}

func TestAIBackfillRechecksArticleVisibility(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	ctx := context.Background()
	repository := postgres.NewEnrichmentBackfillRepository(env.pool)
	request := enrichment.BackfillRequest{Stage: "generation", Mode: "missing-only", Limit: 10, GenerationProfile: "g-v2"}
	candidates, _, err := repository.Candidates(ctx, request, nil, request.Limit, 0)
	if err != nil || len(candidates) == 0 {
		t.Fatalf("候选=%v err=%v", candidates, err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE velis.articles SET status='offline',offline_reason='admin',offline_at=now(),updated_at=now() WHERE id=$1`, candidates[0].ArticleID); err != nil {
		t.Fatal(err)
	}
	changed, err := repository.AdvanceCandidate(ctx, candidates[0].ArticleID, request)
	if err != nil || changed {
		t.Fatalf("文章下架后不应推进任务 changed=%t err=%v", changed, err)
	}
}

func TestAIBackfillCreatesTaskForArticleWithoutExistingSlot(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	ctx := context.Background()
	if _, err := env.pool.Exec(ctx, `DELETE FROM velis.async_tasks WHERE article_id=7101`); err != nil {
		t.Fatal(err)
	}
	service := enrichment.NewAIBackfillService(postgres.NewEnrichmentBackfillRepository(env.pool))
	report, err := service.Run(ctx, enrichment.BackfillRequest{
		Stage:             "generation",
		Mode:              "missing-only",
		ArticleID:         7101,
		GenerationProfile: "g-v2",
	})
	if err != nil || report.Created != 1 || report.Failed != 0 {
		t.Fatalf("首次创建任务 report=%+v err=%v", report, err)
	}
	var aggregateID, stage, status string
	var generation int64
	if err := env.pool.QueryRow(ctx, `SELECT aggregate_id,stage,status,generation FROM velis.async_tasks WHERE article_id=7101`).Scan(&aggregateID, &stage, &status, &generation); err != nil {
		t.Fatal(err)
	}
	if aggregateID != "7101" || stage != "generation" || status != "pending" || generation != 1 {
		t.Fatalf("aggregate_id=%q stage=%s status=%s generation=%d", aggregateID, stage, status, generation)
	}
}

func TestAIBackfillSelectsExactAndNewestAndRepushesFailedTarget(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	ctx := context.Background()
	if _, err := env.pool.Exec(ctx, `UPDATE velis.articles SET published_at=CASE id WHEN 7101 THEN now()-interval '2 days' ELSE now()-interval '1 day' END WHERE id IN (7101,7102)`); err != nil {
		t.Fatal(err)
	}
	service := enrichment.NewAIBackfillService(postgres.NewEnrichmentBackfillRepository(env.pool))
	request := enrichment.BackfillRequest{Stage: "generation", Mode: "missing-only", Limit: 1, Order: "newest", GenerationProfile: "g-v2"}
	report, err := service.Run(ctx, request)
	if err != nil || report.Created != 1 || !report.HasMore {
		t.Fatalf("newest report=%+v err=%v", report, err)
	}
	var generation int64
	var profile *string
	if err := env.pool.QueryRow(ctx, `SELECT generation,generation_profile_version FROM velis.async_tasks WHERE article_id=7102`).Scan(&generation, &profile); err != nil || generation != 3 || profile == nil || *profile != "g-v2" {
		t.Fatalf("newest 未选择 7102: generation=%d profile=%v err=%v", generation, profile, err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE velis.async_tasks SET status='failed',last_error_code='budget_exceeded',last_error_message='模型阶段失败: budget_exceeded' WHERE article_id=7102`); err != nil {
		t.Fatal(err)
	}
	report, err = service.Run(ctx, enrichment.BackfillRequest{Stage: "generation", Mode: "missing-only", ArticleID: 7102, GenerationProfile: "g-v2"})
	if err != nil || report.Created != 1 {
		t.Fatalf("精确重推 failed: report=%+v err=%v", report, err)
	}
	var status string
	var lastError *string
	var repairUsed bool
	if err := env.pool.QueryRow(ctx, `SELECT generation,status,last_error_code,generation_repair_used_at IS NOT NULL FROM velis.async_tasks WHERE article_id=7102`).Scan(&generation, &status, &lastError, &repairUsed); err != nil || generation != 4 || status != "pending" || lastError != nil || repairUsed {
		t.Fatalf("failed 重推结果 generation=%d status=%s err=%v", generation, status, err)
	}
}

func TestAIBackfillAllUsesSnapshotAndRefreshesBothProfiles(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	ctx := context.Background()
	if _, err := env.pool.Exec(ctx, `UPDATE velis.async_tasks SET status='failed',stage='generation',generation_profile_version='g-v1',embedding_profile_version='e-v1' WHERE article_id=7101`); err != nil {
		t.Fatal(err)
	}
	service := enrichment.NewAIBackfillService(postgres.NewEnrichmentBackfillRepository(env.pool))
	report, err := service.Run(ctx, enrichment.BackfillRequest{Stage: "all", Mode: "missing-only", All: true, ConfirmAll: true, GenerationProfile: "g-v2", EmbeddingProfile: "e-v2"})
	if err != nil || report.Created != 2 || report.HasMore {
		t.Fatalf("all report=%+v err=%v", report, err)
	}
	rows, err := env.pool.Query(ctx, `SELECT article_id,stage,status,generation_profile_version,embedding_profile_version FROM velis.async_tasks ORDER BY article_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var articleID int64
		var stage, status string
		var generationProfile, embeddingProfile *string
		if err := rows.Scan(&articleID, &stage, &status, &generationProfile, &embeddingProfile); err != nil {
			t.Fatal(err)
		}
		if stage != "generation" || status != "pending" || generationProfile == nil || *generationProfile != "g-v2" || embeddingProfile == nil || *embeddingProfile != "e-v2" {
			t.Fatalf("article=%d stage=%s status=%s generation=%v embedding=%v", articleID, stage, status, generationProfile, embeddingProfile)
		}
		count++
	}
	if err := rows.Err(); err != nil || count != 2 {
		t.Fatalf("rows=%d err=%v", count, err)
	}
}
