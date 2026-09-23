package integration

import (
	"context"
	"strconv"
	"testing"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/content/markdown"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestUserArticleLifecycleWritesOnlyPublicOutboxFactsAndReplayIsIdempotent(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	const authorID = "54000000-0000-0000-0000-000000000001"
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users(id,username,nickname,password_hash,role,status,created_at,updated_at) VALUES($1,'outbox_author','作者','hash','user','active',$2,$2)`, authorID, now); err != nil {
		t.Fatal(err)
	}
	repository := postgres.NewArticleRepository(env.pool)
	tx := postgres.NewTxManager(env.pool)
	idempotency := idempotencyApp.NewService(postgres.NewIdempotencyRepository(env.pool), tx, 24*time.Hour)
	clock := &articleTestClock{now: now}
	service := articleApp.NewUserServiceWithOutbox(repository, markdown.NewUserRenderer(), postgres.NewArticleAssetRepository(env.pool), idempotency, clock, articleApp.AssetPolicy{MaxImages: 20, MaxTotalBytes: 50 << 20}, postgres.NewOutboxRepository(env.pool))

	draft, _, err := service.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: authorID, IdempotencyKey: "54000000-0000-0000-0000-000000000011", Title: "草稿", Markdown: "草稿正文", InitialStatus: articleDomain.StatusDraft})
	if err != nil {
		t.Fatal(err)
	}
	editedDraft, _, err := service.Update(ctx, articleApp.UpdateUserArticleCommand{AuthorUserID: authorID, ArticleID: draft.Article.ID, ExpectedVersion: 1, IdempotencyKey: "54000000-0000-0000-0000-000000000012", Title: "草稿二版", Markdown: "草稿正文二版"})
	if err != nil {
		t.Fatal(err)
	}
	published, _, err := service.Publish(ctx, articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: draft.Article.ID, ExpectedVersion: editedDraft.Article.LockVersion, IdempotencyKey: "54000000-0000-0000-0000-000000000013"})
	if err != nil {
		t.Fatal(err)
	}
	revised, _, err := service.Update(ctx, articleApp.UpdateUserArticleCommand{AuthorUserID: authorID, ArticleID: draft.Article.ID, ExpectedVersion: published.Article.LockVersion, IdempotencyKey: "54000000-0000-0000-0000-000000000014", Title: "公开修订", Markdown: "公开正文"})
	if err != nil {
		t.Fatal(err)
	}
	same, _, err := service.Update(ctx, articleApp.UpdateUserArticleCommand{AuthorUserID: authorID, ArticleID: draft.Article.ID, ExpectedVersion: revised.Article.LockVersion, IdempotencyKey: "54000000-0000-0000-0000-000000000015", Title: "公开修订", Markdown: "公开正文"})
	if err != nil || same.Article.LockVersion != revised.Article.LockVersion {
		t.Fatalf("无变化编辑=%+v err=%v", same, err)
	}
	offline, _, err := service.Offline(ctx, articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: draft.Article.ID, ExpectedVersion: same.Article.LockVersion, IdempotencyKey: "54000000-0000-0000-0000-000000000016"})
	if err != nil {
		t.Fatal(err)
	}
	offlineEdit, _, err := service.Update(ctx, articleApp.UpdateUserArticleCommand{AuthorUserID: authorID, ArticleID: draft.Article.ID, ExpectedVersion: offline.Article.LockVersion, IdempotencyKey: "54000000-0000-0000-0000-000000000017", Title: "离线修订", Markdown: "离线正文"})
	if err != nil {
		t.Fatal(err)
	}
	republished, _, err := service.Publish(ctx, articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: draft.Article.ID, ExpectedVersion: offlineEdit.Article.LockVersion, IdempotencyKey: "54000000-0000-0000-0000-000000000018"})
	if err != nil {
		t.Fatal(err)
	}
	deleteCommand := articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: draft.Article.ID, ExpectedVersion: republished.Article.LockVersion, IdempotencyKey: "54000000-0000-0000-0000-000000000019"}
	deleted, replayed, err := service.Delete(ctx, deleteCommand)
	if err != nil || replayed {
		t.Fatalf("删除=%+v replay=%t err=%v", deleted, replayed, err)
	}
	if _, replayed, err = service.Delete(ctx, deleteCommand); err != nil || !replayed {
		t.Fatalf("删除重放 replay=%t err=%v", replayed, err)
	}
	if _, _, err = service.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: authorID, IdempotencyKey: "54000000-0000-0000-0000-000000000020", Title: "直接发布", Markdown: "正文", InitialStatus: articleDomain.StatusPublished}); err != nil {
		t.Fatal(err)
	}
	deleteDraft, _, err := service.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: authorID, IdempotencyKey: "54000000-0000-0000-0000-000000000021", Title: "待删除草稿", Markdown: "正文", InitialStatus: articleDomain.StatusDraft})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.Delete(ctx, articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: deleteDraft.Article.ID, ExpectedVersion: 1, IdempotencyKey: "54000000-0000-0000-0000-000000000022"}); err != nil {
		t.Fatal(err)
	}

	rows, err := env.pool.Query(ctx, `SELECT event_type,aggregate_id,aggregate_version,envelope->'payload'->>'reason' FROM velis.outbox_events ORDER BY aggregate_id,aggregate_version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type recorded struct {
		kind, id string
		version  int64
		reason   *string
	}
	var events []recorded
	for rows.Next() {
		var item recorded
		if err := rows.Scan(&item.kind, &item.id, &item.version, &item.reason); err != nil {
			t.Fatal(err)
		}
		events = append(events, item)
	}
	if len(events) != 7 {
		t.Fatalf("事件数量=%d events=%+v", len(events), events)
	}
	wantMain := []string{"article.published.v1", "article.revised.v1", "article.offlined.v1", "article.published.v1", "article.deleted.v1"}
	var main []recorded
	for _, event := range events {
		if event.id == fmtInt64(draft.Article.ID) {
			main = append(main, event)
		}
	}
	if len(main) != len(wantMain) {
		t.Fatalf("主文章事件=%+v", main)
	}
	for index, want := range wantMain {
		if main[index].kind != want {
			t.Fatalf("main[%d]=%s want=%s", index, main[index].kind, want)
		}
	}
	if main[2].reason == nil || *main[2].reason != "author" {
		t.Fatalf("作者下架原因=%v", main[2].reason)
	}
}

func fmtInt64(value int64) string { return strconv.FormatInt(value, 10) }
