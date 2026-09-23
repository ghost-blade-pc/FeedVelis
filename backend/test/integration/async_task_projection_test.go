package integration

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleevent"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asynctask"
	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/content/markdown"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestAsyncTaskProjectionConvergesUnderConcurrencyAndOutOfOrderEvents(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	const authorID = "56000000-0000-0000-0000-000000000001"
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES($1,'projector_author','作者','hash','user','active',$2,$2)`, authorID, now); err != nil {
		t.Fatal(err)
	}
	tx := postgres.NewTxManager(env.pool)
	idempotency := idempotencyApp.NewService(postgres.NewIdempotencyRepository(env.pool), tx, 24*time.Hour)
	clock := &articleTestClock{now: now}
	articles := articleApp.NewUserService(postgres.NewArticleRepository(env.pool), markdown.NewUserRenderer(), postgres.NewArticleAssetRepository(env.pool), idempotency, clock, articleApp.AssetPolicy{MaxImages: 20, MaxTotalBytes: 50 << 20})
	inbox, err := postgres.NewConsumedEventRepository(asynctask.ConsumerName)
	if err != nil {
		t.Fatal(err)
	}
	tasks := postgres.NewAsyncTaskRepository()
	projector := asynctask.NewService(tx, inbox, tasks, func() time.Time { return clock.now })

	published, _, err := articles.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: authorID,
		IdempotencyKey: "56000000-0000-0000-0000-000000000011", Title: "公开", Markdown: "第一版", InitialStatus: articleDomain.StatusPublished})
	if err != nil {
		t.Fatal(err)
	}
	first := mustPublicEvent(t, ctx, articleevent.PublishedType, published.Article, now)
	second := mustPublicEvent(t, ctx, articleevent.PublishedType, published.Article, now)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, event := range []articleevent.Envelope{first, second} {
		wait.Add(1)
		go func(event articleevent.Envelope) {
			defer wait.Done()
			<-start
			_, projectErr := projector.Project(ctx, event)
			results <- projectErr
		}(event)
	}
	close(start)
	wait.Wait()
	close(results)
	for projectErr := range results {
		if projectErr != nil {
			t.Fatal(projectErr)
		}
	}
	assertProjectedTask(t, env, published.Article, "pending", 1, 1)

	clock.now = now.Add(time.Minute)
	revised, _, err := articles.Update(ctx, articleApp.UpdateUserArticleCommand{AuthorUserID: authorID, ArticleID: published.Article.ID,
		ExpectedVersion: published.Article.LockVersion, IdempotencyKey: "56000000-0000-0000-0000-000000000012", Title: "公开", Markdown: "第二版"})
	if err != nil {
		t.Fatal(err)
	}
	revisedEvent := mustPublicEvent(t, ctx, articleevent.RevisedType, revised.Article, clock.now)
	// 先提交一个旧版本事件；投影必须复核数据库当前事实，而不是采用旧 payload。
	if _, err := projector.Project(ctx, mustPublicEvent(t, ctx, articleevent.PublishedType, published.Article, clock.now)); err != nil {
		t.Fatal(err)
	}
	assertProjectedTask(t, env, revised.Article, "pending", 2, revised.Article.LockVersion)

	clock.now = now.Add(2 * time.Minute)
	offline, _, err := articles.Offline(ctx, articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: revised.Article.ID,
		ExpectedVersion: revised.Article.LockVersion, IdempotencyKey: "56000000-0000-0000-0000-000000000013"})
	if err != nil {
		t.Fatal(err)
	}
	offlineEvent, err := articleevent.Offlined(ctx, offline.Article.ID, offline.Article.RevisionID, offline.Article.LockVersion, "author", clock.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projector.Project(ctx, revisedEvent); err != nil {
		t.Fatal(err)
	}
	assertProjectedTask(t, env, offline.Article, "canceled", 3, offline.Article.LockVersion)

	// 离线编辑制造没有事件的 aggregate version 间隔。
	clock.now = now.Add(3 * time.Minute)
	offlineEdit, _, err := articles.Update(ctx, articleApp.UpdateUserArticleCommand{AuthorUserID: authorID, ArticleID: offline.Article.ID,
		ExpectedVersion: offline.Article.LockVersion, IdempotencyKey: "56000000-0000-0000-0000-000000000014", Title: "离线修订", Markdown: "第三版"})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(4 * time.Minute)
	republished, _, err := articles.Publish(ctx, articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: offline.Article.ID,
		ExpectedVersion: offlineEdit.Article.LockVersion, IdempotencyKey: "56000000-0000-0000-0000-000000000015"})
	if err != nil {
		t.Fatal(err)
	}
	republishedEvent := mustPublicEvent(t, ctx, articleevent.PublishedType, republished.Article, clock.now)
	if _, err := projector.Project(ctx, offlineEvent); err != nil {
		t.Fatal(err)
	}
	assertProjectedTask(t, env, republished.Article, "pending", 4, republished.Article.LockVersion)

	clock.now = now.Add(5 * time.Minute)
	deleted, _, err := articles.Delete(ctx, articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: republished.Article.ID,
		ExpectedVersion: republished.Article.LockVersion, IdempotencyKey: "56000000-0000-0000-0000-000000000016"})
	if err != nil {
		t.Fatal(err)
	}
	deletedEvent, err := articleevent.Deleted(ctx, deleted.Article.ID, deleted.Article.RevisionID, deleted.Article.LockVersion, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projector.Project(ctx, republishedEvent); err != nil {
		t.Fatal(err)
	}
	finalTask := assertProjectedTask(t, env, deleted.Article, "canceled", 5, deleted.Article.LockVersion)

	// 逆序、版本间隔和重复事件都不得复活旧状态或推进 generation。
	for _, event := range []articleevent.Envelope{deletedEvent, offlineEvent, revisedEvent, first, republishedEvent} {
		if _, err := projector.Project(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	finalTask = assertProjectedTask(t, env, deleted.Article, "canceled", 5, deleted.Article.LockVersion)
	var slotCount int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.async_tasks WHERE article_id=$1`, deleted.Article.ID).Scan(&slotCount); err != nil || slotCount != 1 {
		t.Fatalf("任务槽位 count=%d err=%v", slotCount, err)
	}
	if err := tx.WithinTransaction(ctx, func(txContext context.Context) error {
		updated, updateErr := tasks.UpdateIfGeneration(txContext, finalTask.ID, finalTask.Generation-1, "pending", clock.now)
		if updateErr != nil {
			return updateErr
		}
		if updated {
			t.Fatal("旧 generation 不应更新任务")
		}
		if _, updateErr = tasks.UpdateIfGeneration(txContext, finalTask.ID, finalTask.Generation, "invalid", clock.now); updateErr == nil {
			t.Fatal("非法任务状态应被拒绝")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func mustPublicEvent(t *testing.T, ctx context.Context, kind string, article articleDomain.StoredArticle, at time.Time) articleevent.Envelope {
	t.Helper()
	fact := articleevent.PublicFact{ArticleID: article.ID, OriginType: string(article.Origin), RevisionID: article.RevisionID,
		RevisionNo: article.RevisionNumber, ContentHash: article.Revision.ContentHash, LockVersion: article.LockVersion}
	var event articleevent.Envelope
	var err error
	if kind == articleevent.RevisedType {
		event, err = articleevent.Revised(ctx, fact, at)
	} else {
		event, err = articleevent.Published(ctx, fact, at)
	}
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func assertProjectedTask(t *testing.T, env *testEnv, article articleDomain.StoredArticle, status string, generation, observed int64) asynctask.Task {
	t.Helper()
	var task asynctask.Task
	err := env.pool.QueryRow(context.Background(), `SELECT id::text,article_id,status,generation,revision_id,revision_no,content_hash,observed_aggregate_version
FROM velis.async_tasks WHERE article_id=$1`, article.ID).Scan(&task.ID, &task.ArticleID, &task.Status, &task.Generation,
		&task.RevisionID, &task.RevisionNo, &task.ContentHash, &task.ObservedVersion)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != status || task.Generation != generation || task.RevisionID != article.RevisionID || task.RevisionNo != article.RevisionNumber ||
		task.ContentHash != article.Revision.ContentHash || task.ObservedVersion != observed || len(strings.TrimSpace(task.ID)) == 0 {
		t.Fatalf("任务未收敛: task=%+v article=%+v wantStatus=%s generation=%d observed=%d", task, article, status, generation, observed)
	}
	return task
}
