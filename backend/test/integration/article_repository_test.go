package integration

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestSourceAndArticleRepositories(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	ctx := context.Background()
	pool := env.pool

	sources := postgres.NewSourceRepository(pool)
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	created, inserted, err := sources.Add(ctx, "https://example.com/feed", "https://example.com/feed", "Example", sourceDomain.DefaultFetchInterval, now)
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
			src, _, err := sources.Add(ctx, "https://example.com/feed", "https://example.com/feed", "Example", sourceDomain.DefaultFetchInterval, now)
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
	// I2 之后不再有 hidden 状态：管理员下架由 offline + offline_reason='admin' 表达。
	// 下架期间的内容更新仍应更新正文，但不得改变可见性——这正是迁移前 hidden 的语义。
	if _, err := pool.Exec(ctx, `UPDATE velis.articles SET status='offline',offline_reason='admin',
published_at=COALESCE(published_at,discovered_at),offline_at=now(),updated_at=now() WHERE id=$1`, articleID); err != nil {
		t.Fatal(err)
	}
	candidate.Article.Title = "下架文章的新内容"
	candidate.Article.ContentHash = strings.Repeat("e", 64)
	result, sameID, err = articles.Upsert(ctx, candidate, now.Add(3*time.Minute))
	if err != nil || result != articleDomain.UpsertUpdated || sameID != articleID {
		t.Fatalf("update offline article: result=%s id=%d err=%v", result, sameID, err)
	}
	var offlineStatus articleDomain.Status
	var offlineReason *string
	if err := pool.QueryRow(ctx, `SELECT status,offline_reason FROM velis.articles WHERE id=$1`, articleID).Scan(&offlineStatus, &offlineReason); err != nil ||
		offlineStatus != articleDomain.StatusOffline || offlineReason == nil || *offlineReason != string(articleDomain.OfflineByAdmin) {
		t.Fatalf("offline status=%s reason=%v err=%v", offlineStatus, offlineReason, err)
	}
	for index := 2; index <= 4; index++ {
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

	_, _, err = sources.Add(ctx, "https://example.org/feed", "https://example.org/feed", "Example 2", sourceDomain.DefaultFetchInterval, now)
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
	manual, _, err := sources.Add(ctx, "https://example.net/feed", "https://example.net/feed", "Example 3", sourceDomain.DefaultFetchInterval, now)
	if err != nil {
		t.Fatal(err)
	}
	oldClaim, err := sources.ClaimByID(ctx, manual.ID, "manual-1", now, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	oldLease, err := oldClaim.CurrentLease()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sources.ClaimByID(ctx, manual.ID, "manual-2", now, now.Add(2*time.Minute)); !errors.Is(err, sourceDomain.ErrLeaseHeld) {
		t.Fatalf("second manual claim err=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE velis.sources SET lease_expires_at=$2 WHERE id=$1`, manual.ID, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	newClaim, err := sources.ClaimByID(ctx, manual.ID, "manual-2", now, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("expired lease was not recoverable: %v", err)
	}
	if err := sources.MarkFailure(ctx, manual.ID, oldLease, sourceDomain.NextFailure(now.Add(time.Minute), 0, "FETCH_FAILED", 0)); !errors.Is(err, sourceDomain.ErrLeaseLost) {
		t.Fatalf("stale lease failure err=%v", err)
	}
	newLease, err := newClaim.CurrentLease()
	if err != nil {
		t.Fatal(err)
	}
	paused, err := sources.Pause(ctx, manual.ID, manual.LockVersion, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if paused.Status != sourceDomain.StatusPaused || paused.LockVersion != manual.LockVersion+1 {
		t.Fatalf("pause: status=%s version=%d", paused.Status, paused.LockVersion)
	}
	metadata := sourceDomain.Metadata{Title: "Example 3"}
	if err := sources.MarkSuccess(ctx, manual.ID, newLease, metadata, now.Add(time.Minute), now.Add(31*time.Minute)); !errors.Is(err, sourceDomain.ErrLeaseLost) {
		t.Fatalf("paused source accepted stale success: %v", err)
	}
	var pausedStatus sourceDomain.Status
	if err := pool.QueryRow(ctx, `SELECT status FROM velis.sources WHERE id=$1`, manual.ID).Scan(&pausedStatus); err != nil || pausedStatus != sourceDomain.StatusPaused {
		t.Fatalf("paused status=%s err=%v", pausedStatus, err)
	}
	if _, err := sources.Resume(ctx, manual.ID, paused.LockVersion, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	finalClaim, err := sources.ClaimByID(ctx, manual.ID, "manual-3", now.Add(2*time.Minute), now.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	finalLease, err := finalClaim.CurrentLease()
	if err != nil {
		t.Fatal(err)
	}
	if err := sources.MarkSuccess(ctx, manual.ID, finalLease, metadata, now.Add(3*time.Minute), now.Add(33*time.Minute)); err != nil {
		t.Fatalf("current lease success: %v", err)
	}
	expiredSource, _, err := sources.Add(ctx, "https://expired.example/feed", "https://expired.example/feed", "Expired", sourceDomain.DefaultFetchInterval, now)
	if err != nil {
		t.Fatal(err)
	}
	expiredClaim, err := sources.ClaimByID(ctx, expiredSource.ID, "worker-expired", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	expiredLease, err := expiredClaim.CurrentLease()
	if err != nil {
		t.Fatal(err)
	}
	if err := sources.MarkNotModified(ctx, expiredSource.ID, expiredLease, nil, nil, now.Add(2*time.Minute), now.Add(32*time.Minute)); !errors.Is(err, sourceDomain.ErrLeaseLost) {
		t.Fatalf("expired lease completion err=%v", err)
	}

	fencedSource, _, err := sources.Add(ctx, "https://fenced.example/feed", "https://fenced.example/feed", "Fenced", sourceDomain.DefaultFetchInterval, now)
	if err != nil {
		t.Fatal(err)
	}
	fencedClaim, err := sources.ClaimByID(ctx, fencedSource.ID, "worker-old", now, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	fencedLease, err := fencedClaim.CurrentLease()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sources.Pause(ctx, fencedSource.ID, fencedClaim.LockVersion, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	atomicCandidate := candidate
	atomicCandidate.Article.SourceID = fencedSource.ID
	atomicCandidate.Article.DedupeKey = strings.Repeat("9", 64)
	atomicCandidate.Article.CanonicalURL = "https://fenced.example/article"
	atomicCandidate.Article.ContentHash = strings.Repeat("8", 64)
	atomicCandidate.Article.Status = articleDomain.StatusPublished
	txManager := postgres.NewTxManager(pool)
	err = txManager.WithinTransaction(ctx, func(txContext context.Context) error {
		result, _, upsertErr := articles.Upsert(txContext, atomicCandidate, now.Add(time.Minute))
		if upsertErr != nil {
			return upsertErr
		}
		if result != articleDomain.UpsertInserted {
			return errors.New("fenced transaction did not insert article fixture")
		}
		return sources.MarkSuccess(txContext, fencedSource.ID, fencedLease, sourceDomain.Metadata{Title: "Fenced"}, now.Add(time.Minute), now.Add(31*time.Minute))
	})
	if !errors.Is(err, sourceDomain.ErrLeaseLost) {
		t.Fatalf("fenced transaction err=%v", err)
	}
	var fencedArticles int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM velis.articles WHERE source_id=$1`, fencedSource.ID).Scan(&fencedArticles); err != nil || fencedArticles != 0 {
		t.Fatalf("fenced articles=%d err=%v", fencedArticles, err)
	}
}
