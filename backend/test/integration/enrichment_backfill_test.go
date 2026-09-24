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
	request.DryRun = false
	report, err = service.Run(ctx, request)
	if err != nil || report.Created != 1 {
		t.Fatalf("write report=%+v err=%v", report, err)
	}
	if err := env.pool.QueryRow(ctx, `SELECT generation,generation_profile_version FROM velis.async_tasks WHERE article_id=7101`).Scan(&generation, &profile); err != nil || generation != 2 || profile == nil || *profile != "g-v2" {
		t.Fatalf("推进结果 generation=%d profile=%v err=%v", generation, profile, err)
	}
	report, err = service.Run(ctx, request)
	if err != nil || report.Skipped != 1 {
		t.Fatalf("重复运行应跳过: report=%+v err=%v", report, err)
	}
	var after int64
	if err := env.pool.QueryRow(ctx, `SELECT generation FROM velis.async_tasks WHERE article_id=7101`).Scan(&after); err != nil || after != generation {
		t.Fatalf("重复运行递增 generation=%d err=%v", after, err)
	}
}

func TestAIBackfillConcurrentRunsAdvanceOnce(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	request := enrichment.BackfillRequest{Stage: "generation", Mode: "missing-only", Limit: 1, GenerationProfile: "g-v2"}
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
	ids, _, err := repository.Candidates(ctx, request)
	if err != nil || len(ids) == 0 {
		t.Fatalf("候选=%v err=%v", ids, err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE velis.articles SET status='offline',offline_reason='admin',offline_at=now(),updated_at=now() WHERE id=$1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	changed, err := repository.AdvanceCandidate(ctx, ids[0], request)
	if err != nil || changed {
		t.Fatalf("文章下架后不应推进任务 changed=%t err=%v", changed, err)
	}
}
