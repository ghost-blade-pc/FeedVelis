package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestUnifiedPublishedQueriesUseStableEffectiveTimeCursor(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
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
	rssOldID := insertLatestRSS(t, ctx, repository, source.ID, "RSS 原站较早", "a", timePointer(now.Add(-2*time.Hour)), now.Add(5*time.Hour))
	rssMissingID := insertLatestRSS(t, ctx, repository, source.ID, "RSS 缺失原站时间", "b", nil, now.Add(2*time.Hour))
	userArticle, err := repository.CreateUserArticle(ctx, authorID, revisionFixture("投稿文章", "f"), true, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	rssNewID := insertLatestRSS(t, ctx, repository, source.ID, "RSS 原站较新", "c", timePointer(now.Add(3*time.Hour)), now.Add(-time.Hour))
	rssSameFirstID := insertLatestRSS(t, ctx, repository, source.ID, "RSS 同时间一", "d", timePointer(now), now.Add(4*time.Hour))
	rssSameSecondID := insertLatestRSS(t, ctx, repository, source.ID, "RSS 同时间二", "e", timePointer(now), now.Add(4*time.Hour))

	wantIDs := []int64{rssNewID, rssMissingID, userArticle.ID, rssSameSecondID, rssSameFirstID, rssOldID}
	var gotIDs []int64
	var cursor *articleDomain.Cursor
	for {
		page, listErr := repository.ListPublished(ctx, cursor, 2)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(page) == 0 {
			break
		}
		for _, item := range page {
			gotIDs = append(gotIDs, item.ID)
		}
		last := page[len(page)-1]
		cursor = &articleDomain.Cursor{SortAt: last.SortAt, ArticleID: last.ID}
	}
	if len(gotIDs) != len(wantIDs) {
		t.Fatalf("跨页条目数 = %v，期望 %v", gotIDs, wantIDs)
	}
	seen := make(map[int64]bool, len(gotIDs))
	for index, id := range gotIDs {
		if id != wantIDs[index] || seen[id] {
			t.Fatalf("有效时间跨页顺序/去重错误: got=%v want=%v", gotIDs, wantIDs)
		}
		seen[id] = true
	}

	updated := revisionFixture("投稿文章已编辑", "9")
	if _, changed, err := repository.UpdateUserRevision(ctx, userArticle.ID, authorID, 1, updated, now.Add(6*time.Hour)); err != nil || !changed {
		t.Fatalf("编辑投稿: changed=%t err=%v", changed, err)
	}
	afterEdit, err := repository.ListPublished(ctx, nil, 3)
	if err != nil || len(afterEdit) != 3 || afterEdit[2].ID != userArticle.ID || !afterEdit[2].SortAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("编辑不得改变 latest 有效时间或位置: %+v err=%v", afterEdit, err)
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

func insertLatestRSS(t *testing.T, ctx context.Context, repository *postgres.ArticleRepository, sourceID int64, title, key string, sourcePublishedAt *time.Time, discoveredAt time.Time) int64 {
	t.Helper()
	candidate := rssCandidate(sourceID, title, key, discoveredAt)
	candidate.Article.DedupeKey = strings.Repeat(key, 64)
	candidate.Article.CanonicalURL = "https://mixed.example/" + key
	candidate.Article.SourcePublishedAt = sourcePublishedAt
	_, id, err := repository.Upsert(ctx, candidate, discoveredAt)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func timePointer(value time.Time) *time.Time {
	return &value
}
