package integration

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

const testCandidateIndex = "velis-articles-v2-20260919t130000z-bbbbbb"

func createPublishedArticle(t *testing.T, env *testEnv, authorID, hashSeed string, now time.Time) int64 {
	t.Helper()
	markdown, html := "正文", "<p>正文</p>"
	stored, err := postgres.NewArticleRepository(env.pool).CreateUserArticle(context.Background(), authorID,
		articleDomain.RevisionData{Title: "投影文章", Markdown: &markdown, SanitizedHTML: &html,
			PlainText: "正文", Excerpt: "正文", Language: "zh-CN",
			ContentHash: strings.Repeat(hashSeed, 64), SanitizerVersion: 1}, true, now)
	if err != nil {
		t.Fatal(err)
	}
	return stored.ID
}

// seedRebuildCandidate 注册一个活动重建候选索引，使已有槽位获得第二条 delivery。
func seedRebuildCandidate(t *testing.T, env *testEnv, candidate string, now time.Time) {
	t.Helper()
	_, err := env.pool.Exec(context.Background(), `INSERT INTO velis.search_index_rebuilds
(id,candidate_index,target_schema_version,schema_identity,phase,start_change_seq,started_at,updated_at)
VALUES('96000000-0000-0000-0000-000000000001',$1,2,'mapping-v2|cjk-v1|dim-3|encoding-v1','snapshot',0,$2,$2)`, candidate, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.NewSearchProjectionRepository(env.pool).EnsureActiveDeliveries(context.Background(), now); err != nil {
		t.Fatal(err)
	}
}

func readDelivery(t *testing.T, env *testEnv, articleID int64, index string) (projectionDomain.Delivery, bool) {
	t.Helper()
	var delivery projectionDomain.Delivery
	var status string
	var result, code, message *string
	err := env.pool.QueryRow(context.Background(), `SELECT status::text,attempt,last_result,last_error_code,last_error_message
FROM velis.search_projection_deliveries WHERE article_id=$1 AND physical_index=$2`, articleID, index).
		Scan(&status, &delivery.Attempt, &result, &code, &message)
	if err != nil {
		return projectionDomain.Delivery{}, false
	}
	delivery.Status = projectionDomain.Status(status)
	if result != nil {
		delivery.LastResult = projectionDomain.Result(*result)
	}
	if code != nil {
		delivery.LastError = &projectionDomain.Failure{Code: *code}
		if message != nil {
			delivery.LastError.Message = *message
		}
	}
	return delivery, true
}

// TestSearchProjectionClaimIsExclusiveAndRecoversExpiredLease 覆盖 2.2 的认领语义：
// 同一槽位不会被两个 Worker 同时认领，过期租约可被安全重试，旧 token 无法写回。
func TestSearchProjectionClaimIsExclusiveAndRecoversExpiredLease(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	seedSearchIndexState(t, env, testPhysicalIndex)
	const authorID = "96000000-0000-0000-0000-000000000002"
	seedIntegrationUser(t, env, authorID, "claim_author", "user", now)
	articleID := createPublishedArticle(t, env, authorID, "a", now)
	repository := postgres.NewSearchProjectionRepository(env.pool)

	const workers = 4
	claimed := make([][]projectionApp.ClaimedJob, workers)
	var wait sync.WaitGroup
	for slot := range workers {
		wait.Add(1)
		go func(slot int) {
			defer wait.Done()
			jobs, err := repository.Claim(ctx, projectionApp.ClaimRequest{Owner: "worker-" + string(rune('a'+slot)), Lease: time.Minute, BatchSize: 10, Now: now})
			if err != nil {
				t.Errorf("并发认领: %v", err)
				return
			}
			claimed[slot] = jobs
		}(slot)
	}
	wait.Wait()
	var job *projectionApp.ClaimedJob
	total := 0
	for _, jobs := range claimed {
		for index := range jobs {
			total++
			current := jobs[index]
			job = &current
		}
	}
	if total != 1 || job == nil || job.ArticleID != articleID {
		t.Fatalf("同一槽位只能被一个 Worker 认领: total=%d job=%+v", total, job)
	}
	if job.Lease.Token == "" || job.Attempt != 1 || len(job.Deliveries) != 1 || job.Deliveries[0].Status != projectionDomain.StatusRunning {
		t.Fatalf("认领必须发放租约并把 delivery 标记为执行中: %+v", job)
	}
	if job.Target.Action != projectionDomain.ActionUpsert || job.Target.RevisionID == 0 {
		t.Fatalf("认领必须带回完整目标身份: %+v", job.Target)
	}

	// 租约未到期时不会被再次认领。
	if again, err := repository.Claim(ctx, projectionApp.ClaimRequest{Owner: "worker-z", Lease: time.Minute, BatchSize: 10, Now: now}); err != nil || len(again) != 0 {
		t.Fatalf("有效租约期间不得重复认领: %+v err=%v", again, err)
	}
	// 认领事务已提交：外部请求期间没有长事务锁。
	probeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	probe, err := env.pool.Begin(probeCtx)
	if err != nil {
		t.Fatal(err)
	}
	var locked int64
	if err := probe.QueryRow(probeCtx, `SELECT article_id FROM velis.search_projection_jobs WHERE article_id=$1 FOR UPDATE`, articleID).Scan(&locked); err != nil {
		_ = probe.Rollback(probeCtx)
		t.Fatalf("认领后仍持有行锁: %v", err)
	}
	if err := probe.Rollback(probeCtx); err != nil {
		t.Fatal(err)
	}

	// 租约过期后另一 Worker 可以重新认领同一 generation。
	later := now.Add(2 * time.Minute)
	recovered, err := repository.Claim(ctx, projectionApp.ClaimRequest{Owner: "worker-recovery", Lease: time.Minute, BatchSize: 10, Now: later})
	if err != nil || len(recovered) != 1 || recovered[0].Lease.Token == job.Lease.Token || recovered[0].Attempt != 2 {
		t.Fatalf("过期租约必须可安全重试: %+v err=%v", recovered, err)
	}
	// 旧执行者的迟到写回不得覆盖新状态。
	stale := projectionApp.Completion{ArticleID: articleID, PhysicalIndex: testPhysicalIndex, Generation: job.Generation,
		LeaseToken: job.Lease.Token, Outcome: projectionApp.DeliveryOutcome{Result: projectionDomain.ResultCreated},
		MaxAttempts: 3, NextAttemptAt: later, Now: later}
	if ok, err := repository.Complete(ctx, stale); err != nil || ok {
		t.Fatalf("旧租约写回必须被拒绝: ok=%t err=%v", ok, err)
	}
	if delivery, _ := readDelivery(t, env, articleID, testPhysicalIndex); delivery.Status != projectionDomain.StatusRunning {
		t.Fatalf("被拒绝的写回不得改变 delivery: %+v", delivery)
	}

	// 新租约写回后槽位收敛。
	fresh := projectionApp.Completion{ArticleID: articleID, PhysicalIndex: testPhysicalIndex, Generation: recovered[0].Generation,
		LeaseToken: recovered[0].Lease.Token, Outcome: projectionApp.DeliveryOutcome{Result: projectionDomain.ResultCreated},
		MaxAttempts: 3, NextAttemptAt: later, Now: later}
	if ok, err := repository.Complete(ctx, fresh); err != nil || !ok {
		t.Fatalf("当前租约写回: ok=%t err=%v", ok, err)
	}
	settled, _ := readProjectionJob(t, env, articleID)
	if settled.Status != "succeeded" {
		t.Fatalf("唯一 delivery 成功后槽位必须收敛: %+v", settled)
	}
	var leaseEmpty bool
	if err := env.pool.QueryRow(ctx, `SELECT lease_token IS NULL AND lease_owner IS NULL FROM velis.search_projection_jobs WHERE article_id=$1`, articleID).Scan(&leaseEmpty); err != nil || !leaseEmpty {
		t.Fatalf("收敛后必须释放租约: empty=%t err=%v", leaseEmpty, err)
	}
}

// TestSearchProjectionPerIndexFailureIsolation 覆盖 2.2 的双索引部分失败：
// 一条 delivery 永久失败只让它自己失败，其他索引独立完成，重试只针对失败项。
func TestSearchProjectionPerIndexFailureIsolation(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	seedSearchIndexState(t, env, testPhysicalIndex)
	const authorID = "96000000-0000-0000-0000-000000000003"
	seedIntegrationUser(t, env, authorID, "isolation_author", "user", now)
	articleID := createPublishedArticle(t, env, authorID, "b", now)
	seedRebuildCandidate(t, env, testCandidateIndex, now)
	repository := postgres.NewSearchProjectionRepository(env.pool)

	claimed, err := repository.Claim(ctx, projectionApp.ClaimRequest{Owner: "worker", Lease: time.Minute, BatchSize: 10, Now: now})
	if err != nil || len(claimed) != 1 || len(claimed[0].Deliveries) != 2 {
		t.Fatalf("重建双写时同一槽位必须有两條 delivery: %+v err=%v", claimed, err)
	}
	job := claimed[0]
	complete := func(index string, outcome projectionApp.DeliveryOutcome) bool {
		t.Helper()
		ok, err := repository.Complete(ctx, projectionApp.Completion{ArticleID: articleID, PhysicalIndex: index,
			Generation: job.Generation, LeaseToken: job.Lease.Token, Outcome: outcome, MaxAttempts: 3,
			NextAttemptAt: now.Add(time.Minute), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	// 当前索引成功、候选索引限流：槽位必须停在可重试状态而不是成功。
	if !complete(testPhysicalIndex, projectionApp.DeliveryOutcome{Result: projectionDomain.ResultCreated}) {
		t.Fatal("当前索引写回失败")
	}
	if !complete(testCandidateIndex, projectionApp.DeliveryOutcome{Result: projectionDomain.ResultRetryable, Code: projectionApp.ErrorThrottled, Message: "429"}) {
		t.Fatal("候选索引写回失败")
	}
	partial, _ := readProjectionJob(t, env, articleID)
	if partial.Status != "retry_wait" {
		t.Fatalf("部分成功后槽位必须等待重试: %+v", partial)
	}
	if delivery, _ := readDelivery(t, env, articleID, testPhysicalIndex); delivery.Status != projectionDomain.StatusSucceeded {
		t.Fatalf("已成功的 delivery 不得被重发: %+v", delivery)
	}
	// 重试只认领尚未完成的 delivery。
	retryAt := now.Add(2 * time.Minute)
	retried, err := repository.Claim(ctx, projectionApp.ClaimRequest{Owner: "worker-retry", Lease: time.Minute, BatchSize: 10, Now: retryAt})
	if err != nil || len(retried) != 1 || len(retried[0].Deliveries) != 1 || retried[0].Deliveries[0].PhysicalIndex != testCandidateIndex {
		t.Fatalf("重试必须只选择未完成的 delivery: %+v err=%v", retried, err)
	}
	if ok, err := repository.Complete(ctx, projectionApp.Completion{ArticleID: articleID, PhysicalIndex: testCandidateIndex,
		Generation: retried[0].Generation, LeaseToken: retried[0].Lease.Token,
		Outcome: projectionApp.DeliveryOutcome{Result: projectionDomain.ResultUpdated}, MaxAttempts: 3,
		NextAttemptAt: retryAt, Now: retryAt}); err != nil || !ok {
		t.Fatalf("候选索引重试写回: ok=%t err=%v", ok, err)
	}
	converged, _ := readProjectionJob(t, env, articleID)
	if converged.Status != "succeeded" {
		t.Fatalf("两条 delivery 都成功后才允许收敛: %+v", converged)
	}

	// 永久失败立即让槽位失败，且其他 delivery 的成功不受影响。
	newState := runProjectionChange(t, env, articleID, authorID, now.Add(3*time.Minute))
	permanentRun, err := repository.Claim(ctx, projectionApp.ClaimRequest{Owner: "worker-fail", Lease: time.Minute, BatchSize: 10, Now: newState})
	if err != nil || len(permanentRun) != 1 {
		t.Fatalf("重新认领: %+v err=%v", permanentRun, err)
	}
	for _, delivery := range permanentRun[0].Deliveries {
		outcome := projectionApp.DeliveryOutcome{Result: projectionDomain.ResultUpdated}
		if delivery.PhysicalIndex == testCandidateIndex {
			outcome = projectionApp.DeliveryOutcome{Result: projectionDomain.ResultPermanent, Code: projectionApp.ErrorMapping, Message: "严格映射失败"}
		}
		if ok, err := repository.Complete(ctx, projectionApp.Completion{ArticleID: articleID, PhysicalIndex: delivery.PhysicalIndex,
			Generation: permanentRun[0].Generation, LeaseToken: permanentRun[0].Lease.Token, Outcome: outcome, MaxAttempts: 3,
			NextAttemptAt: newState, Now: newState}); err != nil || !ok {
			t.Fatalf("写回 %s: ok=%t err=%v", delivery.PhysicalIndex, ok, err)
		}
	}
	failed, _ := readProjectionJob(t, env, articleID)
	if failed.Status != "failed" {
		t.Fatalf("存在永久失败 delivery 时槽位必须失败: %+v", failed)
	}
	if delivery, _ := readDelivery(t, env, articleID, testCandidateIndex); delivery.LastResult != projectionDomain.ResultPermanent ||
		delivery.LastError == nil || delivery.LastError.Code != string(projectionApp.ErrorMapping) {
		t.Fatalf("永久失败必须留下受控分类诊断: %+v", delivery)
	}
	if delivery, _ := readDelivery(t, env, articleID, testPhysicalIndex); delivery.Status != projectionDomain.StatusSucceeded {
		t.Fatalf("其他索引的 delivery 必须独立完成: %+v", delivery)
	}
	// 已失败的槽位不会被自动认领，直到目标再次变化。
	if again, err := repository.Claim(ctx, projectionApp.ClaimRequest{Owner: "worker-idle", Lease: time.Minute, BatchSize: 10, Now: newState.Add(time.Hour)}); err != nil || len(again) != 0 {
		t.Fatalf("失败槽位不得被无限重试: %+v err=%v", again, err)
	}
}

// runProjectionChange 让文章事实变化一次，返回可用于后续认领的时刻。
func runProjectionChange(t *testing.T, env *testEnv, articleID int64, authorID string, now time.Time) time.Time {
	t.Helper()
	markdown, html := "第二版", "<p>第二版</p>"
	stored, _, err := postgres.NewArticleRepository(env.pool).UpdateUserRevision(context.Background(), articleID, authorID, 1,
		articleDomain.RevisionData{Title: "投影文章", Markdown: &markdown, SanitizedHTML: &html, PlainText: "第二版",
			Excerpt: "第二版", Language: "zh-CN", ContentHash: strings.Repeat("c", 64), SanitizerVersion: 1}, now)
	if err != nil || stored.RevisionID == 0 {
		t.Fatalf("修订文章: %+v err=%v", stored, err)
	}
	return now
}

// TestSearchProjectionFailedDeliveryRetryIsBoundedAndExact 覆盖 5.4：
// 重试只重新激活精确范围内的失败投递，且不会把已推进的新 generation 拉回旧失败。
func TestSearchProjectionFailedDeliveryRetryIsBoundedAndExact(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	seedSearchIndexState(t, env, testPhysicalIndex)
	const authorID = "96000000-0000-0000-0000-000000000004"
	seedIntegrationUser(t, env, authorID, "retry_author", "user", now)
	first := createPublishedArticle(t, env, authorID, "d", now)
	second := createPublishedArticle(t, env, authorID, "e", now)
	repository := postgres.NewSearchProjectionRepository(env.pool)

	// 让两篇文章的 delivery 都永久失败。
	fail := func(at time.Time) {
		t.Helper()
		claimed, err := repository.Claim(ctx, projectionApp.ClaimRequest{Owner: "worker", Lease: time.Minute, BatchSize: 10, Now: at})
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range claimed {
			for _, delivery := range job.Deliveries {
				ok, err := repository.Complete(ctx, projectionApp.Completion{ArticleID: job.ArticleID,
					PhysicalIndex: delivery.PhysicalIndex, Generation: job.Generation, LeaseToken: job.Lease.Token,
					Outcome: projectionApp.DeliveryOutcome{Result: projectionDomain.ResultPermanent,
						Code: projectionApp.ErrorMapping, Message: "严格映射失败"},
					MaxAttempts: 3, NextAttemptAt: at, Now: at})
				if err != nil || !ok {
					t.Fatalf("写回永久失败: ok=%t err=%v", ok, err)
				}
			}
		}
	}
	fail(now)
	for _, articleID := range []int64{first, second} {
		if job, _ := readProjectionJob(t, env, articleID); job.Status != "failed" {
			t.Fatalf("文章 %d 必须进入失败状态: %+v", articleID, job)
		}
	}

	// 精确范围：只重试指定文章。
	service := projectionApp.NewRetryService(repository)
	report, err := service.Retry(ctx, projectionApp.RetryScope{ArticleID: first, Limit: 10}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Reactivated) != 1 || report.Reactivated[0].ArticleID != first {
		t.Fatalf("重试必须只覆盖精确范围: %+v", report)
	}
	if job, _ := readProjectionJob(t, env, first); job.Status != "pending" {
		t.Fatalf("被重试的文章必须回到待处理: %+v", job)
	}
	if job, _ := readProjectionJob(t, env, second); job.Status != "failed" {
		t.Fatalf("范围外的失败不得被改动: %+v", job)
	}
	// 已在待处理的投递不会被重复激活。
	again, err := service.Retry(ctx, projectionApp.RetryScope{ArticleID: first, Limit: 10}, now.Add(2*time.Minute))
	if err != nil || len(again.Reactivated) != 0 {
		t.Fatalf("重复重试必须为空: %+v err=%v", again, err)
	}

	// 目标推进后再失败：重试必须作用于新 generation，而不是恢复旧失败。
	next := runProjectionChange(t, env, first, authorID, now.Add(3*time.Minute))
	fail(next.Add(time.Minute))
	advanced, _ := readProjectionJob(t, env, first)
	if advanced.Status != "failed" || advanced.Generation != 2 {
		t.Fatalf("推进后的失败状态: %+v", advanced)
	}
	retried, err := service.Retry(ctx, projectionApp.RetryScope{ArticleID: first, Limit: 10}, next.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(retried.Reactivated) != 1 || retried.Reactivated[0].Generation != advanced.Generation {
		t.Fatalf("重试必须作用于当前 generation: %+v vs %+v", retried.Reactivated, advanced)
	}
	if retried.Superseded != 0 {
		t.Fatalf("范围内不应存在被跳过的旧 generation 失败: %+v", retried)
	}
	// 上限必须被强制：过大的 limit 会被服务拒绝。
	if _, err := service.Retry(ctx, projectionApp.RetryScope{Limit: projectionApp.MaxRetryLimit + 1}, now); !errors.Is(err, projectionApp.ErrInvalidRetryScope) {
		t.Fatalf("超出上限的 limit 必须被拒绝: %v", err)
	}
}
