package integration

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestSourceAndArticleRepositories(t *testing.T) {
	databaseURL := os.Getenv("VELIS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("未设置 VELIS_TEST_DATABASE_URL")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || !strings.HasSuffix(parsed.Path, "_test") {
		t.Fatal("集成测试只允许使用名称以 _test 结尾的数据库")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `TRUNCATE velis.article_contents, velis.articles, velis.sources RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}

	sources := postgres.NewSourceRepository(pool)
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	created, inserted, err := sources.Add(ctx, "https://example.com/feed", "https://example.com/feed", "Example", now)
	if err != nil || !inserted {
		t.Fatalf("add source: inserted=%t err=%v", inserted, err)
	}
	const goroutines = 4
	ids := make(chan int64, goroutines)
	var group sync.WaitGroup
	for range goroutines {
		group.Add(1)
		go func() {
			defer group.Done()
			src, _, err := sources.Add(ctx, "https://example.com/feed", "https://example.com/feed", "Example", now)
			if err != nil {
				t.Errorf("duplicate add: %v", err)
				return
			}
			ids <- src.ID
		}()
	}
	group.Wait()
	close(ids)
	for id := range ids {
		if id != created.ID {
			t.Fatalf("duplicate id=%d want=%d", id, created.ID)
		}
	}

	articles := postgres.NewArticleRepository(pool)
	candidate := articleDomain.Candidate{
		Article: articleDomain.Article{
			SourceID: created.ID, DedupeKey: strings.Repeat("a", 64), CanonicalURL: "https://example.com/a",
			Title: "文章", Excerpt: "摘要", Language: "zh-CN", DiscoveredAt: now,
			ContentHash: strings.Repeat("b", 64), Status: articleDomain.StatusPublished, LastSeenAt: now,
		},
		Content: articleDomain.Content{PlainText: "正文", SanitizerVersion: 1},
	}
	result, articleID, err := articles.Upsert(ctx, candidate, now)
	if err != nil || result != articleDomain.UpsertInserted {
		t.Fatalf("insert article: result=%s err=%v", result, err)
	}
	var originalUpdatedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM velis.articles WHERE id=$1`, articleID).Scan(&originalUpdatedAt); err != nil {
		t.Fatal(err)
	}
	result, sameID, err := articles.Upsert(ctx, candidate, now.Add(time.Minute))
	if err != nil || result != articleDomain.UpsertUnchanged || sameID != articleID {
		t.Fatalf("unchanged article: result=%s id=%d err=%v", result, sameID, err)
	}
	var unchangedUpdatedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM velis.articles WHERE id=$1`, articleID).Scan(&unchangedUpdatedAt); err != nil || !unchangedUpdatedAt.Equal(originalUpdatedAt) {
		t.Fatalf("unchanged updated_at=%v original=%v err=%v", unchangedUpdatedAt, originalUpdatedAt, err)
	}
	candidate.Article.Title = "新标题"
	candidate.Article.ContentHash = strings.Repeat("c", 64)
	result, sameID, err = articles.Upsert(ctx, candidate, now.Add(2*time.Minute))
	if err != nil || result != articleDomain.UpsertUpdated || sameID != articleID {
		t.Fatalf("update article: result=%s id=%d err=%v", result, sameID, err)
	}
	for index := 2; index <= 3; index++ {
		candidate.Article.DedupeKey = strings.Repeat(string(rune('a'+index)), 64)
		candidate.Article.CanonicalURL = "https://example.com/article-" + string(rune('0'+index))
		candidate.Article.ContentHash = strings.Repeat(string(rune('d'+index)), 64)
		if result, _, err := articles.Upsert(ctx, candidate, now); err != nil || result != articleDomain.UpsertInserted {
			t.Fatalf("insert page fixture %d: result=%s err=%v", index, result, err)
		}
	}
	firstPage, err := articles.ListPublished(ctx, nil, 2)
	if err != nil || len(firstPage) != 2 {
		t.Fatalf("first page: items=%+v err=%v", firstPage, err)
	}
	secondPage, err := articles.ListPublished(ctx, &articleDomain.Cursor{SortAt: firstPage[1].SortAt, ArticleID: firstPage[1].ID}, 2)
	if err != nil || len(secondPage) != 1 || secondPage[0].ID == firstPage[0].ID || secondPage[0].ID == firstPage[1].ID {
		t.Fatalf("second page: items=%+v err=%v", secondPage, err)
	}

	_, _, err = sources.Add(ctx, "https://example.org/feed", "https://example.org/feed", "Example 2", now)
	if err != nil {
		t.Fatal(err)
	}
	claimedIDs := make(chan int64, 2)
	for worker := range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			claimed, err := sources.ClaimDue(ctx, "worker-"+string(rune('0'+worker)), now, now.Add(2*time.Minute), 20)
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			for _, src := range claimed {
				claimedIDs <- src.ID
			}
		}()
	}
	group.Wait()
	close(claimedIDs)
	seen := make(map[int64]bool)
	for id := range claimedIDs {
		if seen[id] {
			t.Fatalf("source %d was claimed twice", id)
		}
		seen[id] = true
	}
	if len(seen) != 2 {
		t.Fatalf("claimed=%v", seen)
	}
	manual, _, err := sources.Add(ctx, "https://example.net/feed", "https://example.net/feed", "Example 3", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sources.ClaimByID(ctx, manual.ID, "manual-1", now, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := sources.ClaimByID(ctx, manual.ID, "manual-2", now, now.Add(2*time.Minute)); !errors.Is(err, sourceDomain.ErrLeaseHeld) {
		t.Fatalf("second manual claim err=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE velis.sources SET lease_expires_at=$2 WHERE id=$1`, manual.ID, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := sources.ClaimByID(ctx, manual.ID, "manual-2", now, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("expired lease was not recoverable: %v", err)
	}
}
