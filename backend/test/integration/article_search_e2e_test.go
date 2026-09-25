package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

// TestArticleSearchEndToEnd 用真实 PostgreSQL 与真实 OpenSearch 验证查询适配器、
// PIT 游标和事实源可见性复核组成的完整读取链路。
func TestArticleSearchEndToEnd(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	const authorID = "9a000000-0000-0000-0000-000000000001"
	seedIntegrationUser(t, env, authorID, "search_author", "user", now)
	stack := newProjectionE2E(t, env, 3)

	ids := make([]int64, 0, 3)
	for _, seed := range []string{"m", "n", "o"} {
		ids = append(ids, createPublishedArticle(t, env, authorID, seed, now))
	}
	stack.drain(t, ctx)
	if err := stack.client.Refresh(ctx, stack.index); err != nil {
		t.Fatalf("刷新搜索索引: %v", err)
	}

	codec, err := searchApp.NewCursorCodec([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	service := searchApp.NewService(stack.client, postgres.NewArticleRepository(env.pool), codec, nil,
		searchApp.Config{PITKeepAlive: time.Minute, CandidateBatchSize: 2, MaxCandidatesPerRequest: 10})

	first, err := service.Search(ctx, searchApp.Request{Q: "投影文章", Limit: 1})
	if err != nil || len(first.Items) != 1 || !first.HasMore || first.NextCursor == nil {
		t.Fatalf("搜索第一页失败: page=%+v err=%v", first, err)
	}
	second, err := service.Search(ctx, searchApp.Request{Q: "投影文章", Limit: 1, Cursor: *first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("PIT 下一页必须稳定推进: first=%+v second=%+v err=%v", first, second, err)
	}

	// 不等待投影删除，直接下架搜索第一页命中的文章；PostgreSQL 复核必须立即隐藏它。
	repository := postgres.NewArticleRepository(env.pool)
	reason := articleDomain.OfflineByAuthor
	if _, err := repository.SetArticleState(ctx, first.Items[0].ID, 1, articleDomain.StatusOffline, &reason, nil, nil, nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	afterOffline, err := service.Search(ctx, searchApp.Request{Q: "投影文章", Limit: 3})
	if err != nil || len(afterOffline.Items) != 2 {
		t.Fatalf("下架后的事实源复核失败: page=%+v err=%v", afterOffline, err)
	}
	for _, item := range afterOffline.Items {
		if item.ID == first.Items[0].ID {
			t.Fatalf("搜索泄露下架文章 %d", item.ID)
		}
	}
	if len(ids) != 3 { // 保留显式断言，避免种子数量被无意改变。
		t.Fatalf("测试种子数量错误: %d", len(ids))
	}
}
