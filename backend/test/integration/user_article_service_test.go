package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/content/markdown"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestUserArticleCreateDraftPublishOwnershipAndVisibility(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	const authorID = "51000000-0000-0000-0000-000000000001"
	const otherID = "51000000-0000-0000-0000-000000000002"
	for _, user := range []struct{ id, username, nickname string }{{authorID, "content_author", "作者"}, {otherID, "other_author", "他人"}} {
		if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES ($1,$2,$3,'hash','user','active',$4,$4)`, user.id, user.username, user.nickname, now); err != nil {
			t.Fatal(err)
		}
	}
	repository := postgres.NewArticleRepository(env.pool)
	idempotency := idempotencyApp.NewService(postgres.NewIdempotencyRepository(env.pool), postgres.NewTxManager(env.pool), 24*time.Hour)
	clock := &articleTestClock{now: now}
	service := articleApp.NewUserService(repository, markdown.NewUserRenderer(), postgres.NewArticleAssetRepository(env.pool), idempotency, clock, articleApp.AssetPolicy{MaxImages: 20, MaxTotalBytes: 50 << 20})

	draft, replayed, err := service.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: authorID,
		IdempotencyKey: "51000000-0000-0000-0000-000000000011", InitialStatus: articleDomain.StatusDraft})
	if err != nil || replayed || draft.Article.Status != articleDomain.StatusDraft || draft.Article.PublishedAt != nil {
		t.Fatalf("空草稿 = %+v replayed=%t err=%v", draft, replayed, err)
	}
	if _, err := service.Get(ctx, otherID, draft.Article.ID); !errors.Is(err, articleDomain.ErrNotFound) {
		t.Fatalf("非作者私有读取错误 = %v", err)
	}

	published, _, err := service.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: authorID,
		IdempotencyKey: "51000000-0000-0000-0000-000000000012", Title: "公开文章", Markdown: "公开正文", InitialStatus: articleDomain.StatusPublished})
	if err != nil || published.Article.PublishedAt == nil || !published.Article.PublishedAt.Equal(now) {
		t.Fatalf("直接发布 = %+v err=%v", published, err)
	}
	public, err := repository.GetPublished(ctx, published.Article.ID)
	if err != nil || public.Item.Title != "公开文章" || public.SanitizedHTML == nil {
		t.Fatalf("匿名即时读取 = %+v err=%v", public, err)
	}
	listed, err := service.List(ctx, authorID, "", 20)
	if err != nil || len(listed.Items) != 2 || listed.Items[0].ID != published.Article.ID || listed.HasMore || listed.NextCursor != nil {
		t.Fatalf("本人列表 = %+v err=%v", listed, err)
	}

	const foreignAssetID = "51000000-0000-0000-0000-000000000020"
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.article_assets
(id,owner_user_id,object_key,status,content_type,size_bytes,width,height,quota_counted_at,confirmed_at,created_at,updated_at)
VALUES ($1,$2,'foreign/object','ready','image/png',100,1,1,$3,$3,$3,$3)`, foreignAssetID, otherID, now); err != nil {
		t.Fatal(err)
	}
	_, _, err = service.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: authorID,
		IdempotencyKey: "51000000-0000-0000-0000-000000000013", Title: "越权", Markdown: "![](asset:" + foreignAssetID + ")", InitialStatus: articleDomain.StatusPublished})
	if !errors.Is(err, articleApp.ErrAssetOwnership) {
		t.Fatalf("他人资产错误 = %v", err)
	}
	var count int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.articles WHERE author_user_id=$1`, authorID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("资产校验失败必须回滚文章：count=%d err=%v", count, err)
	}
}

func TestUserArticleMutationsAreAtomicIdempotentAndTerminal(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	const authorID = "52000000-0000-0000-0000-000000000001"
	const adminID = "52000000-0000-0000-0000-000000000002"
	for _, user := range []struct{ id, username, nickname, role string }{
		{authorID, "mutation_author", "作者", "user"}, {adminID, "mutation_admin", "管理员", "admin"},
	} {
		if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES ($1,$2,$3,'hash',$4,'active',$5,$5)`, user.id, user.username, user.nickname, user.role, now); err != nil {
			t.Fatal(err)
		}
	}
	repository := postgres.NewArticleRepository(env.pool)
	idempotency := idempotencyApp.NewService(postgres.NewIdempotencyRepository(env.pool), postgres.NewTxManager(env.pool), 24*time.Hour)
	clock := &articleTestClock{now: now}
	service := articleApp.NewUserService(repository, markdown.NewUserRenderer(), postgres.NewArticleAssetRepository(env.pool), idempotency, clock, articleApp.AssetPolicy{MaxImages: 20, MaxTotalBytes: 50 << 20})
	created, _, err := service.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: authorID,
		IdempotencyKey: "52000000-0000-0000-0000-000000000011", Title: "初始", Markdown: "初始正文", InitialStatus: articleDomain.StatusPublished})
	if err != nil {
		t.Fatal(err)
	}

	update := articleApp.UpdateUserArticleCommand{AuthorUserID: authorID, ArticleID: created.Article.ID, ExpectedVersion: 1,
		IdempotencyKey: "52000000-0000-0000-0000-000000000012", Title: "已更新", Markdown: "新正文"}
	first, replayed, err := service.Update(ctx, update)
	if err != nil || replayed || first.Article.LockVersion != 2 || first.Article.RevisionNumber != 2 {
		t.Fatalf("首次编辑 = %+v replay=%t err=%v", first, replayed, err)
	}
	retry, replayed, err := service.Update(ctx, update)
	if err != nil || !replayed || retry.Article.LockVersion != first.Article.LockVersion || retry.Article.RevisionID != first.Article.RevisionID {
		t.Fatalf("响应丢失重试 = %+v replay=%t err=%v", retry, replayed, err)
	}
	update.IdempotencyKey = "52000000-0000-0000-0000-000000000013"
	if _, _, err := service.Update(ctx, update); !errors.Is(err, articleDomain.ErrVersionConflict) {
		t.Fatalf("旧 If-Match 错误 = %v", err)
	}
	clock.now = now.Add(30 * time.Second)
	authorOffline, _, err := service.Offline(ctx, articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: created.Article.ID, ExpectedVersion: 2,
		IdempotencyKey: "52000000-0000-0000-0000-000000000018"})
	if err != nil || authorOffline.Article.Status != articleDomain.StatusOffline || authorOffline.Article.OfflineReason == nil || *authorOffline.Article.OfflineReason != articleDomain.OfflineByAuthor {
		t.Fatalf("作者下架 = %+v err=%v", authorOffline, err)
	}
	republished, _, err := service.Publish(ctx, articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: created.Article.ID, ExpectedVersion: 3,
		IdempotencyKey: "52000000-0000-0000-0000-000000000019"})
	if err != nil || republished.Article.Status != articleDomain.StatusPublished || republished.Article.LockVersion != 4 || republished.Article.PublishedAt == nil || !republished.Article.PublishedAt.Equal(*created.Article.PublishedAt) {
		t.Fatalf("作者重新发布 = %+v err=%v", republished, err)
	}

	reason := articleDomain.OfflineByAdmin
	actor := adminID
	adminOfflineAt := now.Add(time.Minute)
	offline, err := repository.SetArticleState(ctx, created.Article.ID, 4, articleDomain.StatusOffline, &reason, &actor, nil, nil, adminOfflineAt)
	if err != nil || offline.LockVersion != 5 {
		t.Fatalf("管理员下架 = %+v err=%v", offline, err)
	}
	clock.now = now.Add(2 * time.Minute)
	edited, _, err := service.Update(ctx, articleApp.UpdateUserArticleCommand{AuthorUserID: authorID, ArticleID: created.Article.ID, ExpectedVersion: 5,
		IdempotencyKey: "52000000-0000-0000-0000-000000000014", Title: "离线编辑", Markdown: "离线新正文"})
	if err != nil || edited.Article.Status != articleDomain.StatusOffline || edited.Article.LockVersion != 6 {
		t.Fatalf("管理员下架后编辑 = %+v err=%v", edited, err)
	}
	if _, _, err := service.Publish(ctx, articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: created.Article.ID, ExpectedVersion: 6,
		IdempotencyKey: "52000000-0000-0000-0000-000000000015"}); !errors.Is(err, articleDomain.ErrAdminOffline) {
		t.Fatalf("管理员锁定发布错误 = %v", err)
	}
	deleted, _, err := service.Delete(ctx, articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: created.Article.ID, ExpectedVersion: 6,
		IdempotencyKey: "52000000-0000-0000-0000-000000000016"})
	if err != nil || deleted.Article.Status != articleDomain.StatusDeleted || deleted.Article.LockVersion != 7 {
		t.Fatalf("软删除 = %+v err=%v", deleted, err)
	}
	if _, err := service.Get(ctx, authorID, created.Article.ID); !errors.Is(err, articleDomain.ErrNotFound) {
		t.Fatalf("删除终态私有读取错误 = %v", err)
	}
	if _, _, err := service.Publish(ctx, articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: created.Article.ID, ExpectedVersion: 7,
		IdempotencyKey: "52000000-0000-0000-0000-000000000017"}); !errors.Is(err, articleDomain.ErrNotFound) {
		t.Fatalf("删除终态发布错误 = %v", err)
	}
	if created.Article.PublishedAt == nil || first.Article.PublishedAt == nil || !created.Article.PublishedAt.Equal(*first.Article.PublishedAt) {
		t.Fatalf("编辑改变了固定发布时间: created=%v updated=%v", created.Article.PublishedAt, first.Article.PublishedAt)
	}
}

func TestAdminArticleOfflineRestoreAndVisibilityBoundaries(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	const authorID = "53000000-0000-0000-0000-000000000001"
	const adminID = "53000000-0000-0000-0000-000000000002"
	for _, user := range []struct{ id, username, nickname, role string }{
		{authorID, "boundary_author", "作者", "user"}, {adminID, "boundary_admin", "管理员", "admin"},
	} {
		if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES ($1,$2,$3,'hash',$4,'active',$5,$5)`, user.id, user.username, user.nickname, user.role, now); err != nil {
			t.Fatal(err)
		}
	}
	repository := postgres.NewArticleRepository(env.pool)
	idempotency := idempotencyApp.NewService(postgres.NewIdempotencyRepository(env.pool), postgres.NewTxManager(env.pool), 24*time.Hour)
	clock := &articleTestClock{now: now}
	outbox := postgres.NewOutboxRepository(env.pool)
	users := articleApp.NewUserServiceWithOutbox(repository, markdown.NewUserRenderer(), postgres.NewArticleAssetRepository(env.pool), idempotency, clock, articleApp.AssetPolicy{MaxImages: 20, MaxTotalBytes: 50 << 20}, outbox)
	admins := articleApp.NewAdminServiceWithOutbox(repository, idempotency, clock, outbox)
	draft, _, err := users.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: authorID,
		IdempotencyKey: "53000000-0000-0000-0000-000000000011", InitialStatus: articleDomain.StatusDraft})
	if err != nil {
		t.Fatal(err)
	}
	published, _, err := users.Create(ctx, articleApp.CreateUserArticleCommand{AuthorUserID: authorID,
		IdempotencyKey: "53000000-0000-0000-0000-000000000012", Title: "公开", Markdown: "正文", InitialStatus: articleDomain.StatusPublished})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := admins.Offline(ctx, articleApp.AdminArticleCommand{Actor: articleApp.AdminActor{UserID: authorID, Role: accountDomain.RoleUser},
		ArticleID: published.Article.ID, ExpectedVersion: 1, IdempotencyKey: "53000000-0000-0000-0000-000000000013"}); !errors.Is(err, articleDomain.ErrForbidden) {
		t.Fatalf("普通用户管理员操作错误 = %v", err)
	}
	admin := articleApp.AdminActor{UserID: adminID, Role: accountDomain.RoleAdmin}
	if _, _, err := admins.Offline(ctx, articleApp.AdminArticleCommand{Actor: admin, ArticleID: draft.Article.ID,
		ExpectedVersion: 1, IdempotencyKey: "53000000-0000-0000-0000-000000000014"}); !errors.Is(err, articleDomain.ErrNotFound) {
		t.Fatalf("管理员不应读取草稿: %v", err)
	}
	clock.now = now.Add(time.Minute)
	offline, _, err := admins.Offline(ctx, articleApp.AdminArticleCommand{Actor: admin, ArticleID: published.Article.ID,
		ExpectedVersion: 1, IdempotencyKey: "53000000-0000-0000-0000-000000000015"})
	if err != nil || offline.Article.OfflineReason == nil || *offline.Article.OfflineReason != articleDomain.OfflineByAdmin || offline.Article.LockVersion != 2 {
		t.Fatalf("管理员下架 = %+v err=%v", offline, err)
	}
	if _, err := repository.GetPublished(ctx, published.Article.ID); !errors.Is(err, articleDomain.ErrNotFound) {
		t.Fatalf("管理员下架后匿名读取错误 = %v", err)
	}
	clock.now = now.Add(2 * time.Minute)
	restored, _, err := admins.Restore(ctx, articleApp.AdminArticleCommand{Actor: admin, ArticleID: published.Article.ID,
		ExpectedVersion: 2, IdempotencyKey: "53000000-0000-0000-0000-000000000016"})
	if err != nil || restored.Article.Status != articleDomain.StatusPublished || restored.Article.PublishedAt == nil || !restored.Article.PublishedAt.Equal(*published.Article.PublishedAt) {
		t.Fatalf("管理员恢复 = %+v err=%v", restored, err)
	}
	authorOffline, _, err := users.Offline(ctx, articleApp.UserArticleStateCommand{AuthorUserID: authorID, ArticleID: published.Article.ID,
		ExpectedVersion: 3, IdempotencyKey: "53000000-0000-0000-0000-000000000017"})
	if err != nil || authorOffline.Article.LockVersion != 4 {
		t.Fatalf("作者下架 = %+v err=%v", authorOffline, err)
	}
	if _, _, err := admins.Restore(ctx, articleApp.AdminArticleCommand{Actor: admin, ArticleID: published.Article.ID,
		ExpectedVersion: 4, IdempotencyKey: "53000000-0000-0000-0000-000000000018"}); !errors.Is(err, articleDomain.ErrNotFound) {
		t.Fatalf("管理员不得恢复作者下架文章: %v", err)
	}
	rows, err := env.pool.Query(ctx, `SELECT event_type, aggregate_version,
envelope->'payload'->>'revision_id', envelope->'payload'->>'reason'
FROM velis.outbox_events WHERE aggregate_id=$1 ORDER BY aggregate_version`, fmtInt64(published.Article.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type eventFact struct {
		kind, revisionID string
		version          int64
		reason           *string
	}
	var events []eventFact
	for rows.Next() {
		var event eventFact
		if err := rows.Scan(&event.kind, &event.version, &event.revisionID, &event.reason); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	wantTypes := []string{"article.published.v1", "article.offlined.v1", "article.published.v1", "article.offlined.v1"}
	wantReasons := []*string{nil, stringPointer("admin"), nil, stringPointer("author")}
	if len(events) != len(wantTypes) {
		t.Fatalf("事件数量=%d events=%+v", len(events), events)
	}
	for index, event := range events {
		if event.kind != wantTypes[index] || event.version != int64(index+1) || event.revisionID != fmtInt64(published.Article.RevisionID) {
			t.Fatalf("events[%d]=%+v", index, event)
		}
		if (event.reason == nil) != (wantReasons[index] == nil) || event.reason != nil && *event.reason != *wantReasons[index] {
			t.Fatalf("events[%d].reason=%v", index, event.reason)
		}
	}
}

type articleTestClock struct{ now time.Time }

func (c *articleTestClock) Now() time.Time { return c.now }
