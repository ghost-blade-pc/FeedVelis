package integration

import (
	"context"
	"errors"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"reflect"
	"strings"
	"testing"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleread"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestCacheReadPostgresFactsAndFragmentsMatchOriginal(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	ctx := context.Background()
	repo := postgres.NewArticleRepository(env.pool)
	manager := postgres.NewTxManager(env.pool)
	g, e := testGenerationID, testEmbeddingID
	insertGenerationResult(t, env, g, "p1", 7101, 7111)
	insertEmbeddingResult(t, env, e, g, 7101, 7111)
	setCurrentSelection(t, env, 7101, 7111, &g, &e)
	const author = "c3000000-0000-0000-0000-000000000001"
	seedIntegrationUser(t, env, author, "cache_read_author", "user", fixedNow())
	userArticle := createPublishedArticle(t, env, author, "a", fixedNow())
	source, _, err := postgres.NewSourceRepository(env.pool).Add(ctx, "https://example.com/cache-feed", "https://example.com/cache-feed", "来源", sourceDomain.DefaultFetchInterval, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	_, rssID, err := repo.Upsert(ctx, rssCandidate(source.ID, "RSS 卡片", "b", fixedNow()), fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{userArticle, rssID, 7102, 7101, 999999}
	original, err := repo.ListPublishedWithIdentity(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	service := articleread.NewService(repo, manager, articlecache.Disabled{}, nil, 100, nil)
	actual, err := service.ListPublishedWithIdentity(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[int64]any{}
	for _, item := range original {
		wanted[item.Item.ID] = item
	}
	for _, item := range actual {
		if !reflect.DeepEqual(wanted[item.Item.ID], item) {
			t.Fatalf("新旧公开契约不一致: 原=%+v 新=%+v", wanted[item.Item.ID], item)
		}
	}
	if len(actual) != len(original) || actual[0].Item.ID != userArticle {
		t.Fatal("输入顺序未恢复", actual)
	}
	facts, err := repo.ListPublicFacts(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range facts {
		if fact.Item.Title != "" || fact.Item.Excerpt != "" || fact.Item.Enhancement != nil {
			t.Fatal("事实查询读取了卡片载荷")
		}
	}
	if _, err := repo.ListPublicFacts(ctx, make([]int64, 101)); err == nil {
		t.Fatal("事实批次未限制")
	}
	if _, err := repo.ListPublicFragments(ctx, make([]articlecache.CardIdentity, 101)); err == nil {
		t.Fatal("片段批次未限制")
	}
	if _, err := env.pool.Exec(ctx, `UPDATE velis.users SET nickname='新昵称' WHERE id=$1`, author); err != nil {
		t.Fatal(err)
	}
	actual, err = service.ListPublishedWithIdentity(ctx, []int64{userArticle})
	if err != nil || len(actual) != 1 || actual[0].Item.Author.Nickname != "新昵称" {
		t.Fatal(actual, err)
	}
}

func TestCacheReadSnapshotConcurrentSelectionAndRevision(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	ctx := context.Background()
	repo := postgres.NewArticleRepository(env.pool)
	manager := postgres.NewTxManager(env.pool)
	for _, mode := range []string{"generation", "revision"} {
		t.Run(mode, func(t *testing.T) {
			// 先建立旧 generation，再在事实查询后提交新选择或修订。
			g := testGenerationID
			insertGenerationResultIfMissing(t, env, g, 7101, 7111)
			setCurrentSelection(t, env, 7101, 7111, &g, nil)
			err := manager.WithinReadSnapshot(ctx, func(view context.Context, allowed bool) error {
				if !allowed {
					t.Fatal("自有快照禁止缓存")
				}
				facts, err := repo.ListPublicFacts(view, []int64{7101})
				if err != nil {
					return err
				}
				old := facts[0].Identity
				if mode == "generation" {
					next := "c3000000-0000-0000-0000-000000000002"
					insertGenerationResult(t, env, next, "p2", 7101, 7111)
					setCurrentSelection(t, env, 7101, 7111, &next, nil)
				} else {
					if _, err := env.pool.Exec(ctx, `INSERT INTO velis.article_versions(id,article_id,revision_no,title,raw_description,raw_content,sanitized_html,plain_text,excerpt,language,content_hash,sanitizer_version,created_at) OVERRIDING SYSTEM VALUE VALUES(7113,7101,2,'新修订','','','<p>新</p>','新','新','zh-CN',repeat('c',64),1,now()); UPDATE velis.articles SET current_revision_id=7113,lock_version=lock_version+1 WHERE id=7101`); err != nil {
						return err
					}
				}
				fragments, err := repo.ListPublicFragments(view, []articlecache.CardIdentity{old})
				if err != nil {
					return err
				}
				if len(fragments) != 1 || fragments[0].Identity != old {
					t.Fatal("并发写混合了快照身份", fragments)
				}
				again, err := repo.ListPublicFacts(view, []int64{7101})
				if err != nil {
					return err
				}
				if again[0].Identity != old {
					t.Fatal("快照事实变化")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			current, err := repo.ListPublicFacts(ctx, []int64{7101})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "revision" && current[0].Identity.RevisionID != 7113 {
				t.Fatal("提交后未看见新修订")
			}
		})
	}
}

func insertGenerationResultIfMissing(t *testing.T, env *testEnv, id string, article, revision int64) {
	t.Helper()
	var exists bool
	if err := env.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM velis.ai_generation_results WHERE id=$1)`, id).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		insertGenerationResult(t, env, id, "p1", article, revision)
	}
}

func TestCacheReadExistingWriteTransactionBypassesPublicCache(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	manager := postgres.NewTxManager(env.pool)
	repo := postgres.NewArticleRepository(env.pool)
	cache := &countingPublicCache{}
	service := articleread.NewService(repo, manager, cache, nil, 100, nil)
	rolledBack := errors.New("外层回滚")
	err := manager.WithinTransaction(context.Background(), func(write context.Context) error {

		_, _, err := repo.UpdateUserRevision(write, 7101, "71000000-0000-0000-0000-000000000001", 1, articleDomain.RevisionData{Title: "未提交标题", PlainText: "未提交正文", Excerpt: "未提交摘录", Language: "zh-CN", ContentHash: strings.Repeat("c", 64), SanitizerVersion: 1}, fixedNow())
		if err != nil {
			return err
		}
		current, err := service.ListPublishedWithIdentity(write, []int64{7101})
		if err != nil {
			return err
		}
		if len(current) != 1 || current[0].Item.Title != "未提交标题" {
			t.Fatal("写事务未复用视图", current)
		}
		return rolledBack
	})
	if !errors.Is(err, rolledBack) || cache.gets != 0 || cache.puts != 0 {
		t.Fatal("写事务公开缓存泄漏", err, cache)
	}
}

type countingPublicCache struct {
	articlecache.Disabled
	gets, puts int
}

func (c *countingPublicCache) GetCards(ctx context.Context, ids []articlecache.CardIdentity) (map[articlecache.CardIdentity]articlecache.CardFragment, error) {
	c.gets++
	return c.Disabled.GetCards(ctx, ids)
}
func (c *countingPublicCache) PutCards(context.Context, []articlecache.CardWrite) error {
	c.puts++
	return nil
}

func TestCacheReadPostgresSnapshotIsReadOnly(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	repo := postgres.NewArticleRepository(env.pool)
	manager := postgres.NewTxManager(env.pool)
	err := manager.WithinReadSnapshot(context.Background(), func(view context.Context, _ bool) error {
		_, err := repo.SetArticleState(view, 7101, 1, articleDomain.StatusDeleted, nil, nil, nil, nil, fixedNow())
		return err
	})
	if err == nil {
		t.Fatal("只读快照允许写入")
	}
	facts, err := repo.ListPublicFacts(context.Background(), []int64{7101})
	if err != nil || len(facts) != 1 {
		t.Fatal("只读快照改变公开状态", facts, err)
	}
}
