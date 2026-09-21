package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	sourceApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/source"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/fetcher/httpfeed"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

const adminSourceActor = "51000000-0000-0000-0000-0000000000a1"

func seedAdminActor(t *testing.T, env *testEnv, now time.Time) {
	t.Helper()
	if _, err := env.pool.Exec(context.Background(), `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES ($1,'source_admin','来源管理员','hash','admin','active',$2,$2)`, adminSourceActor, now); err != nil {
		t.Fatal(err)
	}
}

func newAdminSourceService(env *testEnv, fetcher ports.FeedFetcher, parser ports.FeedParser, now time.Time) *sourceApp.AdminService {
	repository := postgres.NewSourceRepository(env.pool)
	runs := postgres.NewFetchRunRepository(env.pool)
	txManager := postgres.NewTxManager(env.pool)
	clock := &articleTestClock{now: now}
	articles := articleApp.NewService(postgres.NewArticleRepository(env.pool), httpfeed.NewSanitizer(), clock, txManager)
	idempotency := idempotencyApp.NewService(postgres.NewIdempotencyRepository(env.pool), txManager, 24*time.Hour)
	fetch := sourceApp.NewService(repository, runs, fetcher, parser, articles, clock, txManager, nil)
	return sourceApp.NewAdminService(repository, runs, fetch, idempotency, clock)
}

// TestAdminSourceCreateVersionAndDuplicateURL 覆盖新增、重复 URL 与乐观锁。
func TestAdminSourceCreateVersionAndDuplicateURL(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	seedAdminActor(t, env, now)
	service := newAdminSourceService(env, &staticFetcher{}, &singleItemParser{}, now)

	command := sourceApp.CreateCommand{ActorUserID: adminSourceActor,
		IdempotencyKey: "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a1001", FeedURL: "https://admin.example/feed"}
	created, replayed, err := service.Create(ctx, command)
	if err != nil || replayed || !created.Created || created.Source.FetchInterval != sourceDomain.DefaultFetchInterval {
		t.Fatalf("新增来源 = %+v replayed=%t err=%v", created, replayed, err)
	}
	// 同键重放：不重复创建。
	if _, replayed, err := service.Create(ctx, command); err != nil || !replayed {
		t.Fatalf("重放 replayed=%t err=%v", replayed, err)
	}
	var count int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.sources`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("来源数 = %d err=%v", count, err)
	}
	// 等价 URL（大小写与默认端口差异）返回既有来源而不是第二个调度身份。
	duplicate := sourceApp.CreateCommand{ActorUserID: adminSourceActor,
		IdempotencyKey: "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a1002", FeedURL: "HTTPS://ADMIN.EXAMPLE:443/feed"}
	existing, _, err := service.Create(ctx, duplicate)
	if err != nil || existing.Created || existing.Source.ID != created.Source.ID {
		t.Fatalf("重复 URL = %+v err=%v", existing, err)
	}

	// 周期修改要求当前版本；旧版本返回版本冲突。
	interval := sourceApp.SourceCommand{ActorUserID: adminSourceActor, SourceID: created.Source.ID,
		ExpectedVersion: created.Source.LockVersion, Interval: 2 * time.Hour,
		IdempotencyKey: "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a1003"}
	updated, _, err := service.SetInterval(ctx, interval)
	if err != nil || updated.FetchInterval != 2*time.Hour || updated.FeedURL != created.Source.FeedURL {
		t.Fatalf("修改周期 = %+v err=%v", updated, err)
	}
	interval.IdempotencyKey, interval.ExpectedVersion = "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a1004", created.Source.LockVersion
	if _, _, err := service.SetInterval(ctx, interval); !errors.Is(err, sourceDomain.ErrVersionConflict) {
		t.Fatalf("旧版本修改 err = %v", err)
	}
}

// TestAdminFetchNowReplaysSameRunAndReportsRunning 覆盖同键重试的两种情形。
func TestAdminFetchNowReplaysSameRunAndReportsRunning(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	seedAdminActor(t, env, now)
	fetcher := &staticFetcher{response: ports.FetchResponse{Body: []byte("<rss/>")}}
	service := newAdminSourceService(env, fetcher, &singleItemParser{}, now)

	created, _, err := service.Create(ctx, sourceApp.CreateCommand{ActorUserID: adminSourceActor,
		IdempotencyKey: "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a2001", FeedURL: "https://manual.example/feed"})
	if err != nil {
		t.Fatal(err)
	}
	fetch := sourceApp.FetchCommand{ActorUserID: adminSourceActor,
		IdempotencyKey: "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a2002", SourceID: created.Source.ID}
	first, err := service.FetchNow(ctx, fetch)
	if err != nil || first.Run.Status != sourceDomain.RunSucceeded || first.Replayed || first.Running {
		t.Fatalf("首次手动抓取 = %+v err=%v", first, err)
	}
	if first.Run.Trigger != sourceDomain.TriggerManual || first.Run.ActorUserID == nil ||
		*first.Run.ActorUserID != adminSourceActor || first.Run.Inserted != 1 {
		t.Fatalf("运行记录 = %+v", first.Run)
	}
	// 完成后同键重试：重放同一运行，不再发起第二次抓取。
	replayed, err := service.FetchNow(ctx, fetch)
	if err != nil || !replayed.Replayed || replayed.Run.ID != first.Run.ID || fetcher.calls != 1 {
		t.Fatalf("重放 = %+v err=%v calls=%d", replayed, err, fetcher.calls)
	}
	var runs int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.source_fetch_runs`).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("运行记录数 = %d err=%v", runs, err)
	}

	// 首次仍在执行时同键重试：真实阻塞住第一次抓取，再并发提交同一个键。
	blocking := &blockingFetcher{started: make(chan struct{}), release: make(chan struct{}),
		response: ports.FetchResponse{Body: []byte("<rss/>")}}
	concurrentService := newAdminSourceService(env, blocking, &singleItemParser{}, now)
	pending := sourceApp.FetchCommand{ActorUserID: adminSourceActor,
		IdempotencyKey: "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a2003", SourceID: created.Source.ID}
	done := make(chan sourceApp.FetchRunResult, 1)
	go func() {
		result, _ := concurrentService.FetchNow(ctx, pending)
		done <- result
	}()
	<-blocking.started

	running, err := concurrentService.FetchNow(ctx, pending)
	if err != nil || !running.Running {
		t.Fatalf("运行中的同键重试 = %+v err=%v", running, err)
	}
	close(blocking.release)
	inFlight := <-done
	if inFlight.Run.ID != running.Run.ID {
		t.Fatalf("两次请求的运行 ID 不一致: %s / %s", inFlight.Run.ID, running.Run.ID)
	}
	if blocking.calls != 1 {
		t.Fatalf("同键重试不得发起第二次抓取，实际 %d 次", blocking.calls)
	}
}

// blockingFetcher 在释放前一直阻塞，用于制造「首次执行仍在进行中」的真实窗口。
type blockingFetcher struct {
	started  chan struct{}
	release  chan struct{}
	response ports.FetchResponse
	calls    int
}

func (f *blockingFetcher) Fetch(context.Context, ports.FetchRequest) (ports.FetchResponse, error) {
	f.calls++
	close(f.started)
	<-f.release
	return f.response, nil
}

// TestAdminHistoryPagination 覆盖历史分页与游标。
func TestAdminHistoryPagination(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	seedAdminActor(t, env, now)
	fetcher := &staticFetcher{response: ports.FetchResponse{Body: []byte("<rss/>")}}
	service := newAdminSourceService(env, fetcher, &singleItemParser{}, now)

	created, _, err := service.Create(ctx, sourceApp.CreateCommand{ActorUserID: adminSourceActor,
		IdempotencyKey: "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a3001", FeedURL: "https://history.example/feed"})
	if err != nil {
		t.Fatal(err)
	}
	for index, key := range []string{"6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a3002", "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a3003"} {
		_ = index
		if _, err := service.FetchNow(ctx, sourceApp.FetchCommand{ActorUserID: adminSourceActor,
			IdempotencyKey: key, SourceID: created.Source.ID}); err != nil {
			t.Fatal(err)
		}
	}

	first, err := service.History(ctx, sourceApp.HistoryCommand{SourceID: created.Source.ID, Limit: 1})
	if err != nil || len(first.Items) != 1 || !first.HasMore || first.NextCursor == nil {
		t.Fatalf("首页 = %+v err=%v", first, err)
	}
	second, err := service.History(ctx, sourceApp.HistoryCommand{SourceID: created.Source.ID,
		Limit: 1, Cursor: *first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("次页 = %+v err=%v", second, err)
	}
	if _, err := service.History(ctx, sourceApp.HistoryCommand{SourceID: created.Source.ID, Cursor: "%%%"}); !errors.Is(err, sourceDomain.ErrInvalidRunCursor) {
		t.Fatalf("非法游标 err = %v", err)
	}
}
