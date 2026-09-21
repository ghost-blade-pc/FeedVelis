package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

// seedFetchSource 新增来源并认领一次，返回来源与它当前的租约 generation。
func seedFetchSource(t *testing.T, env *testEnv, rawURL string, now time.Time) (sourceDomain.Source, int64) {
	t.Helper()
	ctx := context.Background()
	sources := postgres.NewSourceRepository(env.pool)
	created, _, err := sources.Add(ctx, rawURL, rawURL, "运行历史", sourceDomain.DefaultFetchInterval, now)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := sources.ClaimByID(ctx, created.ID, "worker-1", now, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if claimed.LeaseGeneration <= 0 {
		t.Fatalf("认领必须推进租约 generation，实际 %d", claimed.LeaseGeneration)
	}
	return claimed, claimed.LeaseGeneration
}

func startRun(t *testing.T, repository *postgres.FetchRunRepository, sourceID, generation int64, trigger sourceDomain.FetchTrigger, actor *string, startedAt time.Time, index int) sourceDomain.FetchRun {
	t.Helper()
	run, err := sourceDomain.NewFetchRun(fmt.Sprintf("6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a%04d", index),
		sourceID, trigger, actor, generation, startedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Start(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestFetchRunRecordsEveryOutcome(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	ctx := context.Background()
	now := fixedNow()
	env.resetAccounts(t)
	// 手动触发需要真实的操作者账户（外键约束）。
	actor := "51000000-0000-0000-0000-000000000001"
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES ($1,'run_actor','操作者','hash','admin','active',$2,$2)`, actor, now); err != nil {
		t.Fatal(err)
	}
	source, generation := seedFetchSource(t, env, "https://runs.example/feed", now)
	repository := postgres.NewFetchRunRepository(env.pool)

	// 成功入库、304、失败、中止四种终态都能落库并读回。
	succeeded := startRun(t, repository, source.ID, generation, sourceDomain.TriggerScheduled, nil, now, 1)
	if err := succeeded.Succeed(sourceDomain.FetchRunStats{Inserted: 2, Updated: 1, Unchanged: 3, Skipped: 1},
		now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, succeeded); err != nil {
		t.Fatal(err)
	}

	notModified := startRun(t, repository, source.ID, generation, sourceDomain.TriggerManual, &actor, now.Add(time.Minute), 2)
	if err := notModified.Succeed(sourceDomain.FetchRunStats{NotModified: true}, now.Add(time.Minute+time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, notModified); err != nil {
		t.Fatal(err)
	}

	failed := startRun(t, repository, source.ID, generation, sourceDomain.TriggerScheduled, nil, now.Add(2*time.Minute), 3)
	if err := failed.Fail("SSRF_BLOCKED", now.Add(2*time.Minute+time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, failed); err != nil {
		t.Fatal(err)
	}

	aborted := startRun(t, repository, source.ID, generation, sourceDomain.TriggerScheduled, nil, now.Add(3*time.Minute), 4)
	if err := aborted.Abort(now.Add(3*time.Minute + time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, aborted); err != nil {
		t.Fatal(err)
	}

	runs, err := repository.ListBySource(ctx, source.ID, nil, 10)
	if err != nil || len(runs) != 4 {
		t.Fatalf("历史条数 = %d err=%v", len(runs), err)
	}
	// 倒序：最新的在前。
	if runs[0].ID != aborted.ID || runs[3].ID != succeeded.ID {
		t.Fatalf("排序 = %s ... %s", runs[0].ID, runs[3].ID)
	}
	byID := make(map[string]sourceDomain.FetchRun, len(runs))
	for _, run := range runs {
		byID[run.ID] = run
	}
	if stored := byID[succeeded.ID]; stored.Status != sourceDomain.RunSucceeded || stored.Inserted != 2 ||
		stored.Updated != 1 || stored.Unchanged != 3 || stored.Skipped != 1 || stored.NotModified {
		t.Fatalf("成功记录 = %+v", stored)
	}
	if stored := byID[notModified.ID]; !stored.NotModified || stored.Trigger != sourceDomain.TriggerManual ||
		stored.ActorUserID == nil || *stored.ActorUserID != actor {
		t.Fatalf("304 记录 = %+v", stored)
	}
	if stored := byID[failed.ID]; stored.Status != sourceDomain.RunFailed || stored.ErrorCode == nil ||
		*stored.ErrorCode != "SSRF_BLOCKED" {
		t.Fatalf("失败记录 = %+v", stored)
	}
	if stored := byID[aborted.ID]; stored.Status != sourceDomain.RunAborted || stored.ErrorCode != nil {
		t.Fatalf("中止记录 = %+v", stored)
	}
}

func TestFetchRunSingleRunningPerSourceAndFencing(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	ctx := context.Background()
	now := fixedNow()
	source, generation := seedFetchSource(t, env, "https://fencing.example/feed", now)
	repository := postgres.NewFetchRunRepository(env.pool)

	running := startRun(t, repository, source.ID, generation, sourceDomain.TriggerScheduled, nil, now, 1)
	// 同一来源同时只允许一个运行中的记录。
	if err := repository.Start(ctx, mustRun(t, source.ID, generation, now.Add(time.Minute), 2)); err == nil {
		t.Fatal("同一来源不得有第二个运行中的记录")
	}

	// 租约被重新认领后 generation 前进，旧运行的完成写入被拒绝。
	sources := postgres.NewSourceRepository(env.pool)
	if _, err := env.pool.Exec(ctx, `UPDATE velis.sources SET lease_expires_at=$2 WHERE id=$1`,
		source.ID, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := sources.ClaimByID(ctx, source.ID, "worker-2", now.Add(2*time.Minute), now.Add(4*time.Minute))
	if err != nil || reclaimed.LeaseGeneration <= generation {
		t.Fatalf("重新认领 = %+v err=%v", reclaimed, err)
	}
	if err := running.Succeed(sourceDomain.FetchRunStats{}, now.Add(2*time.Minute+time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(ctx, running); !errors.Is(err, sourceDomain.ErrLeaseLost) {
		t.Fatalf("旧 generation 完成写入 err = %v", err)
	}
	// 崩溃遗留的运行由后续收敛为中止。
	aborted, err := repository.AbortStale(ctx, source.ID, reclaimed.LeaseGeneration, now.Add(3*time.Minute))
	if err != nil || aborted != 1 {
		t.Fatalf("收敛遗留运行 = %d err=%v", aborted, err)
	}
	runs, err := repository.ListBySource(ctx, source.ID, nil, 10)
	if err != nil || len(runs) != 1 || runs[0].Status != sourceDomain.RunAborted {
		t.Fatalf("收敛后的历史 = %+v err=%v", runs, err)
	}
}

func TestFetchRunCursorPagingIsStable(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	ctx := context.Background()
	now := fixedNow()
	source, generation := seedFetchSource(t, env, "https://paging.example/feed", now)
	repository := postgres.NewFetchRunRepository(env.pool)

	// 五条记录：前三条开始时间相同，验证同一时间用运行 ID 兜底。
	for index := 1; index <= 5; index++ {
		startedAt := now
		if index > 3 {
			startedAt = now.Add(time.Duration(index) * time.Minute)
		}
		run := startRun(t, repository, source.ID, generation, sourceDomain.TriggerScheduled, nil, startedAt, index)
		if err := run.Succeed(sourceDomain.FetchRunStats{Inserted: index}, startedAt.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := repository.Complete(ctx, run); err != nil {
			t.Fatal(err)
		}
	}

	seen := make(map[string]bool)
	var cursor *sourceDomain.FetchRunCursor
	pages := 0
	for {
		page, err := repository.ListBySource(ctx, source.ID, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		pages++
		for _, run := range page {
			if seen[run.ID] {
				t.Fatalf("翻页出现重复: %s", run.ID)
			}
			seen[run.ID] = true
		}
		last := page[len(page)-1]
		cursor = &sourceDomain.FetchRunCursor{StartedAt: last.StartedAt, ID: last.ID}
	}
	if len(seen) != 5 || pages != 3 {
		t.Fatalf("翻页结果 = %d 条 / %d 页", len(seen), pages)
	}
}

// TestFetchRunTableCannotStoreSensitivePayloads 固化“历史不保存敏感内容”：
// 表结构里没有任何可以承载正文、请求头或凭据的列。
func TestFetchRunTableCannotStoreSensitivePayloads(t *testing.T) {
	env := newTestEnv(t)
	rows, err := env.pool.Query(context.Background(), `SELECT column_name FROM information_schema.columns
WHERE table_schema='velis' AND table_name='source_fetch_runs' ORDER BY column_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := make([]string, 0)
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, column)
	}
	allowed := map[string]bool{
		"id": true, "source_id": true, "trigger": true, "actor_user_id": true, "status": true,
		"lease_generation": true, "not_modified": true, "inserted_count": true, "updated_count": true,
		"unchanged_count": true, "skipped_count": true, "error_code": true, "started_at": true,
		"completed_at": true, "created_at": true,
	}
	if len(columns) != len(allowed) {
		t.Fatalf("列数 = %d，期望 %d：%v", len(columns), len(allowed), columns)
	}
	for _, column := range columns {
		if !allowed[column] {
			t.Fatalf("出现未预期的列 %q：历史表不得承载正文、请求头或凭据", column)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func mustRun(t *testing.T, sourceID, generation int64, now time.Time, index int) sourceDomain.FetchRun {
	t.Helper()
	run, err := sourceDomain.NewFetchRun(fmt.Sprintf("6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a%04d", index),
		sourceID, sourceDomain.TriggerScheduled, nil, generation, now)
	if err != nil {
		t.Fatal(err)
	}
	return run
}
