package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	sourceApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/source"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/fetcher/httpfeed"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

// staticFetcher 返回固定响应，替代真实网络调用。
type staticFetcher struct {
	response ports.FetchResponse
	err      error
	calls    int
}

func (f *staticFetcher) Fetch(context.Context, ports.FetchRequest) (ports.FetchResponse, error) {
	f.calls++
	if f.err != nil {
		return ports.FetchResponse{}, f.err
	}
	return f.response, nil
}

// singleItemParser 为每个来源产出一个条目，驱动文章修订事务。
type singleItemParser struct{ calls int }

func (p *singleItemParser) Parse(context.Context, []byte, string) (ports.ParsedFeed, error) {
	p.calls++
	id := "item-1"
	return ports.ParsedFeed{Title: "抓取来源", Items: []ports.ParsedItem{{
		ID: &id, URL: "https://source.example/article-1", Title: "条目", Content: stringPointer("<p>正文</p>"),
	}}}, nil
}

func stringPointer(value string) *string { return &value }

func newFetchService(t *testing.T, env *testEnv, fetcher ports.FeedFetcher, parser ports.FeedParser,
	runs sourceDomain.FetchRunRepository, now time.Time) *sourceApp.Service {
	t.Helper()
	articles := articleApp.NewService(postgres.NewArticleRepository(env.pool), httpfeed.NewSanitizer(),
		&articleTestClock{now: now}, postgres.NewTxManager(env.pool))
	return sourceApp.NewService(postgres.NewSourceRepository(env.pool), runs, fetcher, parser,
		articles, &articleTestClock{now: now}, postgres.NewTxManager(env.pool), nil)
}

// TestFetchAbortsCrashLeftoverRunAndRecordsNewOne 验证崩溃遗留运行会被收敛，而不是永久停留在运行中。
func TestFetchAbortsCrashLeftoverRunAndRecordsNewOne(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	ctx := context.Background()
	now := fixedNow()
	sources := postgres.NewSourceRepository(env.pool)
	created, _, err := sources.Add(ctx, "https://crash.example/feed", "https://crash.example/feed",
		"崩溃遗留", sourceDomain.DefaultFetchInterval, now)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟上一次进程在抓取中被杀死：先有一次已过期的认领，运行记录仍停留在 running。
	// 新的认领会推进 generation，使这条遗留运行可被识别为过期。
	crashedClaim, err := sources.ClaimByID(ctx, created.ID, "worker-crashed",
		now.Add(-time.Hour), now.Add(-time.Hour+time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	leftover, err := sourceDomain.NewFetchRun("6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0001", created.ID,
		sourceDomain.TriggerScheduled, nil, crashedClaim.LeaseGeneration, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	runs := postgres.NewFetchRunRepository(env.pool)
	if err := runs.Start(ctx, leftover); err != nil {
		t.Fatal(err)
	}

	fetcher := &staticFetcher{response: ports.FetchResponse{Body: []byte("<rss/>"), FinalURL: created.FeedURL}}
	service := newFetchService(t, env, fetcher, &singleItemParser{}, runs, now)
	outcome, err := service.FetchByID(ctx, created.ID, false, nil)
	if err != nil || outcome.Report.Inserted != 1 {
		t.Fatalf("抓取结果 = %+v err=%v", outcome, err)
	}

	history, err := runs.ListBySource(ctx, created.ID, nil, 10)
	if err != nil || len(history) != 2 {
		t.Fatalf("历史条数 = %d err=%v", len(history), err)
	}
	byID := make(map[string]sourceDomain.FetchRun, len(history))
	for _, run := range history {
		byID[run.ID] = run
	}
	if stored := byID[leftover.ID]; stored.Status != sourceDomain.RunAborted || stored.CompletedAt == nil {
		t.Fatalf("遗留运行未被收敛: %+v", stored)
	}
	var succeeded *sourceDomain.FetchRun
	for _, run := range history {
		if run.Status == sourceDomain.RunSucceeded {
			succeeded = &run
		}
	}
	if succeeded == nil || succeeded.Inserted != 1 || succeeded.Trigger != sourceDomain.TriggerScheduled {
		t.Fatalf("本次运行 = %+v", succeeded)
	}
}

// TestFetchCompletionRollsBackWithRunFailure 验证文章修订、Source 完成与运行结果同事务：
// 运行结果写入失败时三者一并回滚。
func TestFetchCompletionRollsBackWithRunFailure(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	ctx := context.Background()
	now := fixedNow()
	sources := postgres.NewSourceRepository(env.pool)
	created, _, err := sources.Add(ctx, "https://rollback.example/feed", "https://rollback.example/feed",
		"回滚", sourceDomain.DefaultFetchInterval, now)
	if err != nil {
		t.Fatal(err)
	}

	runs := &fencedRunRepository{FetchRunRepository: postgres.NewFetchRunRepository(env.pool), failComplete: true}
	fetcher := &staticFetcher{response: ports.FetchResponse{Body: []byte("<rss/>"), FinalURL: created.FeedURL}}
	service := newFetchService(t, env, fetcher, &singleItemParser{}, runs, now)

	_, err = service.FetchByID(ctx, created.ID, false, nil)
	if !errors.Is(err, sourceDomain.ErrLeaseLost) {
		t.Fatalf("运行结果写入失败 err = %v", err)
	}
	// 条目、Source 完成状态与运行结果都不得留下痕迹。
	var articles int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.articles WHERE source_id=$1`, created.ID).Scan(&articles); err != nil || articles != 0 {
		t.Fatalf("文章数 = %d err=%v", articles, err)
	}
	reloaded, err := sources.Get(ctx, created.ID)
	if err != nil || reloaded.LastCheckedAt != nil || reloaded.LastSuccessAt != nil {
		t.Fatalf("Source 完成状态 = %+v err=%v", reloaded, err)
	}
	history, err := runs.ListBySource(ctx, created.ID, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range history {
		if run.Status != sourceDomain.RunRunning {
			t.Fatalf("运行结果不应落库: %+v", run)
		}
	}
}

// TestFetchFailurePathRecordsRunAndKeepsLeaseLostIdentifiable 验证失败路径的短事务与可识别结果。
func TestFetchFailurePathRecordsRunAndKeepsLeaseLostIdentifiable(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	ctx := context.Background()
	now := fixedNow()
	sources := postgres.NewSourceRepository(env.pool)
	created, _, err := sources.Add(ctx, "https://failure.example/feed", "https://failure.example/feed",
		"失败路径", sourceDomain.DefaultFetchInterval, now)
	if err != nil {
		t.Fatal(err)
	}
	runs := postgres.NewFetchRunRepository(env.pool)

	// 网络层失败：Source 退避与运行失败记录都写入。
	fetcher := &staticFetcher{err: codeError{code: "SSRF_BLOCKED"}}
	service := newFetchService(t, env, fetcher, &singleItemParser{}, runs, now)
	_, err = service.FetchByID(ctx, created.ID, false, nil)
	if sourceApp.ErrorCode(err) != "SSRF_BLOCKED" {
		t.Fatalf("失败分类 = %v", err)
	}
	history, err := runs.ListBySource(ctx, created.ID, nil, 10)
	if err != nil || len(history) != 1 || history[0].Status != sourceDomain.RunFailed {
		t.Fatalf("失败历史 = %+v err=%v", history, err)
	}
	if history[0].ErrorCode == nil || *history[0].ErrorCode != "SSRF_BLOCKED" {
		t.Fatalf("失败错误码 = %+v", history[0])
	}
	reloaded, err := sources.Get(ctx, created.ID)
	if err != nil || reloaded.ConsecutiveFailures != 1 {
		t.Fatalf("失败退避 = %+v err=%v", reloaded, err)
	}

	// 租约已被取代：失败记录无法归因，只暴露可识别的租约丢失。
	stale := &fencedRunRepository{FetchRunRepository: runs, failComplete: true}
	staleService := newFetchService(t, env, fetcher, &singleItemParser{}, stale, now)
	fetcher.err = errors.New("连接被拒绝")
	if _, err := staleService.FetchByID(ctx, created.ID, false, nil); !errors.Is(err, sourceDomain.ErrLeaseLost) {
		t.Fatalf("租约失效时的失败结果 err = %v", err)
	}
}

// TestConcurrentManualFetchConflictsWithScheduledLease 验证手动与定时抓取不会并发访问上游。
func TestConcurrentManualFetchConflictsWithScheduledLease(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	ctx := context.Background()
	now := fixedNow()
	sources := postgres.NewSourceRepository(env.pool)
	created, _, err := sources.Add(ctx, "https://concurrent.example/feed", "https://concurrent.example/feed",
		"并发", sourceDomain.DefaultFetchInterval, now)
	if err != nil {
		t.Fatal(err)
	}
	runs := postgres.NewFetchRunRepository(env.pool)
	service := newFetchService(t, env, &staticFetcher{}, &singleItemParser{}, runs, now)

	// 调度器先认领：手动抓取必须被拒绝。
	claimed, err := service.ClaimDue(ctx, "worker-scheduled", 10, 2*time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("认领 = %d err=%v", len(claimed), err)
	}
	if _, err := service.FetchByID(ctx, created.ID, false, nil); !errors.Is(err, sourceDomain.ErrLeaseHeld) {
		t.Fatalf("占位期间的手动抓取 err = %v", err)
	}
	// 定时路径照常执行并记录运行。
	outcome, err := service.FetchClaimed(ctx, claimed[0])
	if err != nil || outcome.Report.Inserted != 1 {
		t.Fatalf("定时抓取 = %+v err=%v", outcome, err)
	}
	history, err := runs.ListBySource(ctx, created.ID, nil, 10)
	if err != nil || len(history) != 1 || history[0].Trigger != sourceDomain.TriggerScheduled ||
		history[0].Status != sourceDomain.RunSucceeded {
		t.Fatalf("定时历史 = %+v err=%v", history, err)
	}
}

// codeError 模拟取回适配器的受控错误分类。
type codeError struct{ code string }

func (e codeError) Error() string { return e.code + ": 受限地址" }
func (e codeError) Code() string  { return e.code }

// fencedRunRepository 让完成写入失败，用于验证事务边界。
type fencedRunRepository struct {
	sourceDomain.FetchRunRepository
	failComplete bool
}

func (f *fencedRunRepository) Complete(ctx context.Context, run sourceDomain.FetchRun) error {
	if f.failComplete {
		return sourceDomain.ErrLeaseLost
	}
	return f.FetchRunRepository.Complete(ctx, run)
}
