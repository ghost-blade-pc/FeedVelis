package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestUnifiedPublishedQueriesUseStablePublishedCursor(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	const authorID = "60000000-0000-0000-0000-000000000001"
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES ($1,'unified_author','站内作者','hash','user','active',$2,$2)`, authorID, now); err != nil {
		t.Fatal(err)
	}
	source, _, err := postgres.NewSourceRepository(env.pool).Add(ctx, "https://mixed.example/feed", "https://mixed.example/feed", "RSS 来源", sourceDomain.DefaultFetchInterval, now)
	if err != nil {
		t.Fatal(err)
	}
	repository := postgres.NewArticleRepository(env.pool)
	_, rssID, err := repository.Upsert(ctx, rssCandidate(source.ID, "RSS 文章", "e", now), now)
	if err != nil {
		t.Fatal(err)
	}
	userArticle, err := repository.CreateUserArticle(ctx, authorID, revisionFixture("投稿文章", "f"), true, now)
	if err != nil {
		t.Fatal(err)
	}

	first, err := repository.ListPublished(ctx, nil, 1)
	if err != nil || len(first) != 1 || first[0].ID != userArticle.ID || first[0].Origin != articleDomain.OriginUser || first[0].Author == nil || first[0].Author.Nickname != "站内作者" {
		t.Fatalf("混合首页 = %+v err=%v", first, err)
	}
	second, err := repository.ListPublished(ctx, &articleDomain.Cursor{SortAt: first[0].SortAt, ArticleID: first[0].ID}, 2)
	if err != nil || len(second) != 1 || second[0].ID != rssID || second[0].Origin != articleDomain.OriginRSS || second[0].Source.Title != "RSS 来源" {
		t.Fatalf("混合第二页 = %+v err=%v", second, err)
	}

	updated := revisionFixture("投稿文章已编辑", "9")
	if _, changed, err := repository.UpdateUserRevision(ctx, userArticle.ID, authorID, 1, updated, now.Add(time.Hour)); err != nil || !changed {
		t.Fatalf("编辑投稿: changed=%t err=%v", changed, err)
	}
	afterEdit, err := repository.ListPublished(ctx, nil, 2)
	if err != nil || len(afterEdit) != 2 || !afterEdit[0].SortAt.Equal(now) || !afterEdit[1].SortAt.Equal(now) {
		t.Fatalf("编辑不得改变 latest 位置: %+v err=%v", afterEdit, err)
	}
	detail, err := repository.GetPublished(ctx, userArticle.ID)
	if err != nil || detail.Item.Origin != articleDomain.OriginUser || detail.SanitizedHTML == nil {
		t.Fatalf("投稿公开详情 = %+v err=%v", detail, err)
	}

	reason := articleDomain.OfflineByAuthor
	if _, err := repository.SetArticleState(ctx, userArticle.ID, 2, articleDomain.StatusOffline, &reason, nil, nil, nil, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetPublished(ctx, userArticle.ID); !errors.Is(err, articleDomain.ErrNotFound) {
		t.Fatalf("私有状态详情应为 not found: %v", err)
	}
}
