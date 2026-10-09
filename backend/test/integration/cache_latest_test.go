package integration

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleread"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/fetcher/httpfeed"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"github.com/ghost-blade-pc/Velis_Feed/backend/test/testkit/redistest"
)

func TestCacheReadLatestRealRedisCurrentFactsAndPagination(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Hour)
	const author = "c4000000-0000-0000-0000-000000000001"
	seedIntegrationUser(t, env, author, "cache_latest_author", "user", now)
	ids := []int64{}
	for _, hash := range []string{"a", "b", "c", "d", "e"} {
		ids = append(ids, createPublishedArticle(t, env, author, hash, now))
	}
	cache, h := newOwnedReadCache(t, "")
	repo := postgres.NewArticleRepository(env.pool)
	manager := postgres.NewTxManager(env.pool)
	shared := articleread.NewService(repo, manager, cache, nil, 100, nil)
	service := articleApp.NewService(repo, httpfeed.NewSanitizer(), &articleTestClock{now: now}, manager).WithLatestReader(shared)
	original, err := repo.ListPublished(ctx, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.List(ctx, "", 2)
	if err != nil || !first.HasMore || !reflect.DeepEqual(first.Items, original[:2]) {
		t.Fatal(first, err)
	}
	q := articlecache.LatestQuery{Limit: 2}
	key := cache.latestKey(q)
	ttl := h.Client.PTTL(ctx, key).Val()
	warm, err := service.List(ctx, "", 2)
	if err != nil || !reflect.DeepEqual(warm, first) {
		t.Fatal(warm, err)
	}
	if after := h.Client.PTTL(ctx, key).Val(); after > ttl {
		t.Fatal("热点读取延长 TTL", ttl, after)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE velis.users SET nickname='动态昵称' WHERE id=$1`, author); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE velis.articles SET status='offline',offline_reason='author',offline_at=now() WHERE id=$1`, ids[4]); err != nil {
		t.Fatal(err)
	}
	changed, err := service.List(ctx, "", 2)
	if err != nil || len(changed.Items) != 2 || changed.Items[0].ID != ids[3] || changed.Items[0].Author.Nickname != "动态昵称" {
		t.Fatal("旧候选泄露下架或昵称", changed, err)
	}
	next, err := service.List(ctx, *changed.NextCursor, 2)
	if err != nil || next.Items[0].ID != ids[1] || next.Items[1].ID != ids[0] || next.HasMore {
		t.Fatal("lookahead 丢失或重复", next, err)
	}
	// 当前 sort_at 变化必须丢弃整批旧候选；已暖卡片也需重新复核。
	if _, err := env.pool.Exec(ctx, `UPDATE velis.articles SET published_at=published_at-interval '1 day' WHERE id=$1`, ids[3]); err != nil {
		t.Fatal(err)
	}
	changed, err = service.List(ctx, "", 2)
	if err != nil || changed.Items[0].ID != ids[2] || changed.Items[1].ID != ids[1] {
		t.Fatal("排序位置变化混用旧页", changed, err)
	}
	for _, mode := range []string{"corrupt", "clear"} {
		if mode == "corrupt" {
			if err := h.Client.Set(ctx, key, "损坏", time.Second).Err(); err != nil {
				t.Fatal(err)
			}
		} else if err := h.Clear(); err != nil {
			t.Fatal(err)
		}
		actual, err := service.List(ctx, "", 2)
		if err != nil || !reflect.DeepEqual(actual, changed) {
			t.Fatal(mode, actual, err)
		}
	}
	// 外部候选/片段均已暖时，数据库失败仍不能返回公开卡片。
	broken := articleread.NewService(failedReadRepository{Repository: repo}, manager, cache, nil, 100, nil)
	if _, _, _, err := broken.ListLatest(ctx, nil, 2); !errors.Is(err, errReadDatabase) {
		t.Fatal("缓存掩盖数据库失败", err)
	}
}

var errReadDatabase = errors.New("数据库读取失败")

type failedReadRepository struct{ articleread.Repository }

func (failedReadRepository) ListPublicFacts(context.Context, []int64) ([]articleread.CurrentFact, error) {
	return nil, errReadDatabase
}

func TestCacheReadLatestRedisDisconnectedStillUsesPostgres(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	h := redistest.New(t)
	proxy := redistest.NewProxy(t, h.Address)
	cache, _ := newOwnedReadCache(t, proxy.Address())
	repo := postgres.NewArticleRepository(env.pool)
	manager := postgres.NewTxManager(env.pool)
	shared := articleread.NewService(repo, manager, cache, nil, 100, nil)
	expected, _, _, err := shared.ListLatest(context.Background(), nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	proxy.Disconnect(true)
	start := time.Now()
	actual, _, _, err := shared.ListLatest(context.Background(), nil, 1)
	if err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatal(actual, err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("故障没有有界回源")
	}
	proxy.Disconnect(false)
	recovered, _, _, err := shared.ListLatest(context.Background(), nil, 1)
	if err != nil || !reflect.DeepEqual(recovered, expected) {
		t.Fatal(recovered, err)
	}
}
