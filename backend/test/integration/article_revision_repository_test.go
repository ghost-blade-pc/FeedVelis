package integration

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestArticleRepositoryImmutableRevisionsAndOptimisticLock(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 21, 5, 0, 0, 0, time.UTC)
	const authorID = "40000000-0000-0000-0000-000000000001"
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users
(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES ($1,'revision_user','修订用户','hash','user','active',$2,$2)`, authorID, now); err != nil {
		t.Fatal(err)
	}

	repository := postgres.NewArticleRepository(env.pool)
	initial := revisionFixture("初稿", "a")
	created, err := repository.CreateUserArticle(ctx, authorID, initial, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != articleDomain.StatusDraft || created.RevisionNumber != 1 || created.LockVersion != 1 {
		t.Fatalf("创建结果 = %+v", created)
	}

	unchanged, changed, err := repository.UpdateUserRevision(ctx, created.ID, authorID, 1, initial, now.Add(time.Minute))
	if err != nil || changed || unchanged.RevisionNumber != 1 || unchanged.LockVersion != 1 {
		t.Fatalf("无变化编辑 = %+v changed=%t err=%v", unchanged, changed, err)
	}

	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, fixture := range []articleDomain.RevisionData{revisionFixture("标签页甲", "b"), revisionFixture("标签页乙", "c")} {
		fixture := fixture
		group.Add(1)
		go func() {
			defer group.Done()
			_, changed, err := repository.UpdateUserRevision(ctx, created.ID, authorID, 1, fixture, now.Add(2*time.Minute))
			if err == nil && !changed {
				err = errors.New("并发内容变化未创建修订")
			}
			results <- err
		}()
	}
	group.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, articleDomain.ErrVersionConflict):
			conflicts++
		default:
			t.Fatalf("并发编辑意外错误: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("并发结果 success=%d conflict=%d", successes, conflicts)
	}

	current, err := repository.GetUserArticle(ctx, created.ID, authorID)
	if err != nil || current.RevisionNumber != 2 || current.LockVersion != 2 {
		t.Fatalf("当前修订 = %+v err=%v", current, err)
	}
	publishedAt := now.Add(3 * time.Minute)
	stateChanged, err := repository.SetArticleState(ctx, created.ID, 2, articleDomain.StatusPublished, nil, nil, &publishedAt, nil, publishedAt)
	if err != nil || stateChanged.LockVersion != 3 || stateChanged.RevisionNumber != 2 {
		t.Fatalf("状态变化不应创建修订: %+v err=%v", stateChanged, err)
	}
	var revisions int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.article_versions WHERE article_id=$1`, created.ID).Scan(&revisions); err != nil || revisions != 2 {
		t.Fatalf("修订数 = %d err=%v", revisions, err)
	}
	if _, err := repository.GetUserArticle(ctx, created.ID, "40000000-0000-0000-0000-000000000099"); !errors.Is(err, articleDomain.ErrNotFound) {
		t.Fatalf("来源/所有者查询必须互斥且隐藏资源: %v", err)
	}
}

func revisionFixture(title, hashSeed string) articleDomain.RevisionData {
	markdown := "正文 " + title
	html := "<p>" + markdown + "</p>"
	return articleDomain.RevisionData{Title: title, Markdown: &markdown, SanitizedHTML: &html,
		PlainText: markdown, Excerpt: markdown, Language: "zh-CN", ContentHash: strings.Repeat(hashSeed, 64), SanitizerVersion: 1}
}
