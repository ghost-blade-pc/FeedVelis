package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleread"
	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/fetcher/httpfeed"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestCacheReadPostgresAfterCommitNestedRollbackAndFailure(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	manager := postgres.NewTxManager(env.pool)
	repo := postgres.NewArticleRepository(env.pool)
	ctx := context.Background()
	actions := 0
	rollback := errors.New("外层回滚")
	for _, mode := range []string{"rollback", "commit_failure", "success"} {
		before := actions
		err := manager.WithinTransaction(ctx, func(write context.Context) error {
			return manager.WithinTransaction(write, func(nested context.Context) error {
				if err := manager.RegisterAfterCommit(nested, "once", func(after context.Context) error {
					actions++
					facts, err := repo.ListPublicFacts(after, []int64{7101})
					if err != nil || len(facts) != 0 {
						t.Error("回调未看见已提交状态", facts, err)
					}
					return errors.New("提交后动作失败")
				}); err != nil {
					return err
				}
				if err := manager.RegisterAfterCommit(nested, "once", func(context.Context) error { t.Error("去重失败"); return nil }); err != nil {
					return err
				}
				switch mode {
				case "rollback":
					return rollback
				case "commit_failure":
					// FK 失败使真实事务中止；忽略语句错误后 Commit 仍必须失败且不能执行回调。
					_, _ = repo.CreateUserArticle(nested, "00000000-0000-0000-0000-000000000000", articleDomain.RevisionData{Title: "提交失败", ContentHash: strings.Repeat("d", 64), Language: "zh-CN", SanitizerVersion: 1}, true, fixedNow())
					return nil
				default:
					_, err := repo.SetArticleState(nested, 7101, 1, articleDomain.StatusOffline, pointerOfflineReason(articleDomain.OfflineByAuthor), nil, nil, nil, fixedNow())
					return err
				}
			})
		})
		if mode == "success" {
			if err != nil || actions != before+1 {
				t.Fatal(mode, err, actions)
			}
		} else if err == nil || actions != before {
			t.Fatal(mode, err, actions)
		}
	}
}
func pointerOfflineReason(r articleDomain.OfflineReason) *articleDomain.OfflineReason { return &r }

func TestCacheReadRSSOuterRollbackDedupAndFailedInvalidation(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := time.Now().UTC()
	cache, h := newOwnedReadCache(t, "")
	repo := postgres.NewArticleRepository(env.pool)
	manager := postgres.NewTxManager(env.pool)
	source, _, err := postgres.NewSourceRepository(env.pool).Add(ctx, "https://example.com/cache-rss", "https://example.com/cache-rss", "RSS", sourceDomain.DefaultFetchInterval, now)
	if err != nil {
		t.Fatal(err)
	}
	invalidator := articleread.NewInvalidator(manager, cache, ctx, 100*time.Millisecond)
	service := articleApp.NewServiceWithOutbox(repo, httpfeed.NewSanitizer(), &articleTestClock{now: now}, manager, postgres.NewOutboxRepository(env.pool)).WithPublicReadInvalidator(invalidator)
	first := ports.ParsedItem{ID: pointerString("first"), URL: "https://example.com/cache-first", Title: "首篇", Content: pointerString("<p>正文</p>")}
	second := ports.ParsedItem{ID: pointerString("second"), URL: "https://example.com/cache-second", Title: "第二篇", Content: pointerString("<p>正文</p>")}
	oldPage := articlecache.LatestPage{Candidates: []articlecache.Candidate{}, Exhausted: true}
	created := time.Now().UTC().Add(-2 * time.Second)
	for limit := 1; limit <= 50; limit++ {
		q := articlecache.LatestQuery{Limit: limit}
		if err := cache.PutLatest(cache.NewRequest(ctx), q, oldPage, created); err != nil {
			t.Fatal(err)
		}
	}
	rollback := errors.New("RSS 外层回滚")
	err = manager.WithinTransaction(ctx, func(write context.Context) error {
		if _, err := service.Ingest(write, source.ID, []ports.ParsedItem{first, second}); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) || cache.invalidations != 0 {
		t.Fatal("RSS 回滚仍失效", err, cache.invalidations)
	}
	if page, err := repo.ListLatestCandidates(ctx, articlecache.LatestQuery{Limit: 50}); err != nil || len(page.Candidates) != 0 {
		t.Fatal("RSS 事务泄漏", page, err)
	}
	err = manager.WithinTransaction(ctx, func(write context.Context) error {
		_, err := service.Ingest(write, source.ID, []ports.ParsedItem{first, second})
		return err
	})
	if err != nil || cache.invalidations != 1 {
		t.Fatal("嵌套 RSS 未合并一次失效", err, cache.invalidations)
	}
	for limit := 1; limit <= 50; limit++ {
		if n := h.Client.Exists(ctx, cache.latestKey(articlecache.LatestQuery{Limit: limit})).Val(); n != 0 {
			t.Fatal("未覆盖首页 limit", limit)
		}
	}
	// 迟到旧回填只得到原先到期时刻，不重新获得 5 秒寿命。
	q := articlecache.LatestQuery{Limit: 20}
	if err := cache.PutLatest(cache.NewRequest(ctx), q, oldPage, created); err != nil {
		t.Fatal(err)
	}
	if ttl := h.Client.PTTL(ctx, cache.latestKey(q)).Val(); ttl <= 0 || ttl > 3*time.Second {
		t.Fatal("迟到回填延长旧页", ttl)
	}
	before := cache.invalidations
	report, err := service.Ingest(ctx, source.ID, []ports.ParsedItem{first, second})
	if err != nil || report.Unchanged != 2 || cache.invalidations != before {
		t.Fatal("RSS 幂等更新触发失效", report, err)
	}
	cache.invalidateErr = errors.New("失效故障")
	first.Title = "新标题"
	report, err = service.Ingest(ctx, source.ID, []ports.ParsedItem{first})
	if err != nil || report.Updated != 1 || cache.invalidations != before+1 {
		t.Fatal("失效故障改变业务结果", report, err)
	}
	page, err := repo.ListPublished(ctx, nil, 3)
	if err != nil || len(page) != 2 {
		t.Fatal(page, err)
	}
	var count int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.outbox_events`).Scan(&count); err != nil || count != 3 {
		t.Fatal("RSS Outbox 原子性改变", count, err)
	}
}
func pointerString(s string) *string { return &s }

func TestCacheReadUserAdminTransitionsAndIdempotency(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := time.Now().UTC()
	const author = "c5000000-0000-0000-0000-000000000001"
	const admin = "c5000000-0000-0000-0000-000000000002"
	seedIntegrationUser(t, env, author, "cache_write_author", "user", now)
	seedIntegrationUser(t, env, admin, "cache_write_admin", "admin", now)
	cache, _ := newOwnedReadCache(t, "")
	manager := postgres.NewTxManager(env.pool)
	invalidator := articleread.NewInvalidator(manager, cache, ctx, 100*time.Millisecond)
	user := newUserArticleStack(t, env, now).WithPublicReadInvalidator(invalidator)
	result, _, err := user.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: author, IdempotencyKey: "c5000000-0000-0000-0000-000000000101", Title: "草稿", Markdown: strings.Repeat("正文", 100), InitialStatus: articleDomain.StatusDraft})
	if err != nil || cache.invalidations != 0 {
		t.Fatal(result, err)
	}
	command := articleApp.UserArticleStateCommand{AuthorUserID: author, ArticleID: result.Article.ID, ExpectedVersion: result.Article.LockVersion, IdempotencyKey: "c5000000-0000-0000-0000-000000000102"}
	result, replayed, err := user.Publish(ctx, command)
	if err != nil || replayed || cache.invalidations != 1 {
		t.Fatal(result, err)
	}
	if _, replayed, err := user.Publish(ctx, command); err != nil || !replayed || cache.invalidations != 1 {
		t.Fatal("幂等重放再次失效", err)
	}
	edit := articleApp.UpdateUserArticleCommand{AuthorUserID: author, ArticleID: result.Article.ID, ExpectedVersion: result.Article.LockVersion, IdempotencyKey: "c5000000-0000-0000-0000-000000000103", Title: "公开新标题", Markdown: strings.Repeat("正文", 100)}
	result, _, err = user.Update(ctx, edit)
	if err != nil || cache.invalidations != 2 {
		t.Fatal(result, err)
	}
	adminService := articleApp.NewAdminServiceWithOutbox(postgres.NewArticleRepository(env.pool), idempotencyApp.NewService(postgres.NewIdempotencyRepository(env.pool), manager, 24*time.Hour), &articleTestClock{now: now}, postgres.NewOutboxRepository(env.pool)).WithPublicReadInvalidator(invalidator)
	ac := articleApp.AdminArticleCommand{Actor: articleApp.AdminActor{UserID: admin, Role: accountDomain.RoleAdmin}, ArticleID: result.Article.ID, ExpectedVersion: result.Article.LockVersion, IdempotencyKey: "c5000000-0000-0000-0000-000000000104"}
	result, _, err = adminService.Offline(ctx, ac)
	if err != nil || cache.invalidations != 3 {
		t.Fatal(result, err)
	}
	ac.ExpectedVersion = result.Article.LockVersion
	ac.IdempotencyKey = "c5000000-0000-0000-0000-000000000105"
	result, _, err = adminService.Restore(ctx, ac)
	if err != nil || cache.invalidations != 4 {
		t.Fatal(result, err)
	}
	command.ExpectedVersion = result.Article.LockVersion
	command.IdempotencyKey = "c5000000-0000-0000-0000-000000000106"
	if _, _, err := user.Delete(ctx, command); err != nil || cache.invalidations != 5 {
		t.Fatal("公开删除未失效", err)
	}
}
