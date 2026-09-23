package integration

import (
	"context"
	"fmt"
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

type mutatingBackfillRepository struct {
	delegate  *postgres.BackfillRepository
	mutations map[int64]func(context.Context) error
}

func (r *mutatingBackfillRepository) BackfillCandidates(ctx context.Context, limit int) ([]int64, bool, error) {
	return r.delegate.BackfillCandidates(ctx, limit)
}

func (r *mutatingBackfillRepository) LockBackfillCandidate(ctx context.Context, articleID int64) (asynctask.ArticleFact, bool, error) {
	if mutation := r.mutations[articleID]; mutation != nil {
		delete(r.mutations, articleID)
		if err := mutation(ctx); err != nil {
			return asynctask.ArticleFact{}, false, err
		}
	}
	return r.delegate.LockBackfillCandidate(ctx, articleID)
}

func TestArticleBackfillCreatesOnlyStillEligibleFactsAndIsRepeatable(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	const authorID = "58000000-0000-0000-0000-000000000001"
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES($1,'backfill_author','作者','hash','user','active',$2,$2)`, authorID, now); err != nil {
		t.Fatal(err)
	}
	tx := postgres.NewTxManager(env.pool)
	idempotency := idempotencyApp.NewService(postgres.NewIdempotencyRepository(env.pool), tx, 24*time.Hour)
	clock := &articleTestClock{now: now}
	repository := postgres.NewArticleRepository(env.pool)
	plainService := articleApp.NewUserService(repository, markdown.NewUserRenderer(), postgres.NewArticleAssetRepository(env.pool), idempotency, clock, articleApp.AssetPolicy{MaxImages: 20, MaxTotalBytes: 50 << 20})
	outbox := postgres.NewOutboxRepository(env.pool)
	eventService := articleApp.NewUserServiceWithOutbox(repository, markdown.NewUserRenderer(), postgres.NewArticleAssetRepository(env.pool), idempotency, clock, articleApp.AssetPolicy{MaxImages: 20, MaxTotalBytes: 50 << 20}, outbox)
	create := func(index int, title string) articleDomain.StoredArticle {
		t.Helper()
		result, _, err := plainService.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: authorID,
			IdempotencyKey: fmt.Sprintf("58000000-0000-0000-0000-%012d", index), Title: title, Markdown: "正文", InitialStatus: articleDomain.StatusPublished})
		if err != nil {
			t.Fatal(err)
		}
		return result.Article
	}
	eligible := create(11, "需要补录")
	withEvent := create(12, "已有事件")
	withTask := create(13, "已有任务")
	concurrentRevision := create(14, "并发修订")
	concurrentOffline := create(15, "并发下架")

	existingEvent := mustPublicEvent(t, ctx, articleevent.PublishedType, withEvent, now)
	if err := tx.WithinTransaction(ctx, func(txContext context.Context) error { return outbox.Append(txContext, existingEvent) }); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.async_tasks
(id,task_type,aggregate_type,aggregate_id,article_id,revision_id,revision_no,content_hash,status,generation,observed_aggregate_version,created_at,updated_at)
VALUES('58000000-0000-0000-0000-000000000099','article.enrichment','article',$1,$2,$3,$4,$5,'pending',1,$6,$7,$7)`,
		fmtInt64(withTask.ID), withTask.ID, withTask.RevisionID, withTask.RevisionNumber, withTask.Revision.ContentHash, withTask.LockVersion, now); err != nil {
		t.Fatal(err)
	}
	backfillRepository := &mutatingBackfillRepository{delegate: postgres.NewBackfillRepository(env.pool), mutations: map[int64]func(context.Context) error{
		concurrentRevision.ID: func(txContext context.Context) error {
			_, _, err := eventService.Update(txContext, articleApp.UpdateUserArticleCommand{AuthorUserID: authorID, ArticleID: concurrentRevision.ID,
				ExpectedVersion: concurrentRevision.LockVersion, IdempotencyKey: "58000000-0000-0000-0000-000000000021", Title: "并发修订后", Markdown: "新正文"})
			return err
		},
		concurrentOffline.ID: func(txContext context.Context) error {
			_, _, err := eventService.Offline(txContext, articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: concurrentOffline.ID,
				ExpectedVersion: concurrentOffline.LockVersion, IdempotencyKey: "58000000-0000-0000-0000-000000000022"})
			return err
		},
	}}
	service := asynctask.NewBackfillService(backfillRepository, outbox, tx, func() time.Time { return now.Add(time.Hour) })
	report, err := service.Run(ctx, 10)
	if err != nil || report.Created != 1 || report.Skipped != 2 || report.Failed != 0 || report.HasMore {
		t.Fatalf("补录报告=%+v err=%v", report, err)
	}
	var kind string
	var version int64
	err = env.pool.QueryRow(ctx, `SELECT event_type,aggregate_version FROM velis.outbox_events WHERE aggregate_id=$1`, fmtInt64(eligible.ID)).Scan(&kind, &version)
	if err != nil || kind != articleevent.PublishedType || version != eligible.LockVersion {
		t.Fatalf("补录事件 kind=%s version=%d err=%v", kind, version, err)
	}
	second, err := service.Run(ctx, 10)
	if err != nil || second.Created != 0 || second.Failed != 0 {
		t.Fatalf("重复补录=%+v err=%v", second, err)
	}
	var generation int64
	if err := env.pool.QueryRow(ctx, `SELECT generation FROM velis.async_tasks WHERE article_id=$1`, withTask.ID).Scan(&generation); err != nil || generation != 1 {
		t.Fatalf("已有任务 generation=%d err=%v", generation, err)
	}
}
