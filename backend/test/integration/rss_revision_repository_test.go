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

func TestRSSUpsertCreatesImmutableRevisionsAndPreservesVisibility(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 21, 7, 0, 0, 0, time.UTC)
	source, inserted, err := postgres.NewSourceRepository(env.pool).Add(ctx, "https://example.com/feed", "https://example.com/feed", "来源", sourceDomain.DefaultFetchInterval, now)
	if err != nil || !inserted {
		t.Fatalf("创建 Source: inserted=%t err=%v", inserted, err)
	}
	repository := postgres.NewArticleRepository(env.pool)
	candidate := rssCandidate(source.ID, "初版", "a", now)
	result, articleID, err := repository.Upsert(ctx, candidate, now)
	if err != nil || result != articleDomain.UpsertInserted {
		t.Fatalf("首次 RSS upsert: result=%s err=%v", result, err)
	}
	assertRSSState(t, env, articleID, 1, 1, articleDomain.StatusPublished, now)

	result, _, err = repository.Upsert(ctx, candidate, now.Add(time.Minute))
	if err != nil || result != articleDomain.UpsertUnchanged {
		t.Fatalf("不变 RSS upsert: result=%s err=%v", result, err)
	}
	assertRSSState(t, env, articleID, 1, 1, articleDomain.StatusPublished, now)

	candidate.Article.Title = "第二版"
	candidate.Article.ContentHash = strings.Repeat("b", 64)
	result, _, err = repository.Upsert(ctx, candidate, now.Add(2*time.Minute))
	if err != nil || result != articleDomain.UpsertUpdated {
		t.Fatalf("变化 RSS upsert: result=%s err=%v", result, err)
	}
	assertRSSState(t, env, articleID, 2, 2, articleDomain.StatusPublished, now)

	const adminID = "50000000-0000-0000-0000-000000000001"
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES ($1,'rss_admin','RSS 管理员','hash','admin','active',$2,$2)`, adminID, now); err != nil {
		t.Fatal(err)
	}
	reason := articleDomain.OfflineByAdmin
	actor := adminID
	if _, err := repository.SetArticleState(ctx, articleID, 2, articleDomain.StatusOffline, &reason, &actor, nil, nil, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	candidate.Article.Title = "下架期间第三版"
	candidate.Article.ContentHash = strings.Repeat("c", 64)
	result, _, err = repository.Upsert(ctx, candidate, now.Add(4*time.Minute))
	if err != nil || result != articleDomain.UpsertUpdated {
		t.Fatalf("下架 RSS 更新: result=%s err=%v", result, err)
	}
	assertRSSState(t, env, articleID, 3, 4, articleDomain.StatusOffline, now)
	if _, err := repository.GetPublished(ctx, articleID); !errors.Is(err, articleDomain.ErrNotFound) {
		t.Fatalf("管理员下架文章不应匿名可见: %v", err)
	}
}

func rssCandidate(sourceID int64, title, hashSeed string, now time.Time) articleDomain.Candidate {
	html := "<p>正文</p>"
	return articleDomain.Candidate{Article: articleDomain.Article{
		SourceID: sourceID, DedupeKey: strings.Repeat("d", 64), CanonicalURL: "https://example.com/article",
		Title: title, Excerpt: "正文", Language: "zh-CN", DiscoveredAt: now,
		ContentHash: strings.Repeat(hashSeed, 64), Status: articleDomain.StatusPublished, LastSeenAt: now,
	}, Content: articleDomain.Content{SanitizedHTML: &html, PlainText: "正文", SanitizerVersion: 1}}
}

func assertRSSState(t *testing.T, env *testEnv, articleID int64, wantRevisions int, wantLock int64, wantStatus articleDomain.Status, wantPublished time.Time) {
	t.Helper()
	var revisions int
	var lock int64
	var status articleDomain.Status
	var published time.Time
	if err := env.pool.QueryRow(context.Background(), `SELECT count(v.id),a.lock_version,a.status,a.published_at
FROM velis.articles a JOIN velis.article_versions v ON v.article_id=a.id WHERE a.id=$1
GROUP BY a.lock_version,a.status,a.published_at`, articleID).Scan(&revisions, &lock, &status, &published); err != nil {
		t.Fatal(err)
	}
	if revisions != wantRevisions || lock != wantLock || status != wantStatus || !published.Equal(wantPublished) {
		t.Fatalf("RSS 状态 revisions=%d lock=%d status=%s published=%v", revisions, lock, status, published)
	}
}
