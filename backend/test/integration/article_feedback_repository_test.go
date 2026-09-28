package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	feedback "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/articlefeedback"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

func TestArticleFeedbackRepository(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedFeedbackArticle(t, env)
	ctx := context.Background()
	const userA = "72000000-0000-0000-0000-000000000001"
	const userB = "72000000-0000-0000-0000-000000000002"
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES($1,'feedback_test_user_b','测试用户B','hash','user','active',now(),now())`, userB); err != nil {
		t.Fatal(err)
	}
	r := postgres.NewArticleFeedbackRepository(env.pool)
	now := time.Date(2026, 9, 28, 23, 59, 59, 0, time.UTC)
	var group sync.WaitGroup
	for range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := r.RecordRead(ctx, userA, 7201, now); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	assertFeedbackCount(t, env, "article_read_windows", 1)
	if err := r.RecordRead(ctx, userA, 7201, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	assertFeedbackCount(t, env, "article_read_windows", 2)
	if err := r.RecordRead(ctx, userB, 7201, now); err != nil {
		t.Fatal(err)
	}
	assertFeedbackCount(t, env, "article_read_windows", 3)
	for range 8 {
		group.Add(2)
		go func() {
			defer group.Done()
			if err := r.SetFavorite(ctx, userA, 7201, true, now); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer group.Done()
			if err := r.SetNotInterested(ctx, userA, 7201, true, now); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	assertFeedbackCount(t, env, "article_favorites", 1)
	assertFeedbackCount(t, env, "article_not_interested", 1)
	for range 2 {
		if err := r.SetFavorite(ctx, userA, 7201, true, now); err != nil {
			t.Fatal(err)
		}
		if err := r.SetNotInterested(ctx, userA, 7201, true, now); err != nil {
			t.Fatal(err)
		}
	}
	states, err := r.States(ctx, userA, []int64{7201}, now)
	if err != nil || len(states) != 1 || !states[0].Favorited || !states[0].NotInterested {
		t.Fatalf("反馈状态: %+v, %v", states, err)
	}
	initialExpiry := *states[0].NotInterestedExpiresAt
	if err := r.SetNotInterested(ctx, userA, 7201, true, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	states, err = r.States(ctx, userA, []int64{7201}, now.Add(24*time.Hour))
	if err != nil || !states[0].NotInterestedExpiresAt.Equal(initialExpiry) {
		t.Fatalf("重复提交延长了有效期: %+v, %v", states, err)
	}
	states, err = r.States(ctx, userB, []int64{7201}, now)
	if err != nil || len(states) != 1 || states[0].Favorited || states[0].NotInterested {
		t.Fatalf("跨用户泄漏: %+v, %v", states, err)
	}
	states, err = r.States(ctx, userA, []int64{7201}, initialExpiry)
	if err != nil || states[0].NotInterested {
		t.Fatalf("过期状态仍有效: %+v, %v", states, err)
	}
	if err := r.SetNotInterested(ctx, userA, 7201, true, initialExpiry); err != nil {
		t.Fatal(err)
	}
	states, err = r.States(ctx, userA, []int64{7201}, initialExpiry)
	if err != nil || !states[0].NotInterested || !states[0].NotInterestedExpiresAt.Equal(initialExpiry.Add(feedback.NotInterestedRetention)) {
		t.Fatalf("过期重设失败: %+v, %v", states, err)
	}
	if err := r.SetFavorite(ctx, userA, 7201, false, now); err != nil {
		t.Fatal(err)
	}
	if err := r.SetNotInterested(ctx, userA, 7201, false, now); err != nil {
		t.Fatal(err)
	}
	states, err = r.States(ctx, userA, []int64{7201}, now)
	if err != nil || states[0].Favorited || states[0].NotInterested {
		t.Fatalf("撤销失败: %+v, %v", states, err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE velis.articles SET status='offline',offline_reason='author',offline_at=$2 WHERE id=$1`, 7201, now); err != nil {
		t.Fatal(err)
	}
	if err := r.SetFavorite(ctx, userA, 7201, true, now); !errors.Is(err, feedback.ErrArticleNotFound) {
		t.Fatalf("下架写入: %v", err)
	}
	states, err = r.States(ctx, userA, []int64{7201}, now)
	if err != nil || len(states) != 0 {
		t.Fatalf("下架状态泄漏: %+v, %v", states, err)
	}
	assertFeedbackCount(t, env, "article_favorites", 0)
}

func TestArticleFeedbackWriteWaitsForOfflineAndRollsBackOnFailure(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedFeedbackArticle(t, env)
	ctx := context.Background()
	r := postgres.NewArticleFeedbackRepository(env.pool)
	now := fixedNow()
	const userA = "72000000-0000-0000-0000-000000000001"
	const missingUser = "72000000-0000-0000-0000-000000000099"
	if err := r.RecordRead(ctx, missingUser, 7201, now); err == nil {
		t.Fatal("外键失败必须回滚")
	}
	assertFeedbackCount(t, env, "article_read_windows", 0)
	tx, err := env.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE velis.articles SET status='offline',offline_reason='author',offline_at=$2 WHERE id=$1`, 7201, now); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- r.SetFavorite(ctx, userA, 7201, true, now) }()
	select {
	case err := <-result:
		t.Fatalf("下架事务未提交时写入不应完成: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, feedback.ErrArticleNotFound) {
			t.Fatalf("下架提交后写入应拒绝: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("写入等待下架后未结束")
	}
	assertFeedbackCount(t, env, "article_favorites", 0)
}

func assertFeedbackCount(t *testing.T, env *testEnv, table string, want int) {
	t.Helper()
	var count int
	if err := env.pool.QueryRow(context.Background(), "SELECT count(*) FROM velis."+table).Scan(&count); err != nil || count != want {
		t.Fatalf("%s 数量=%d，期望=%d，错误=%v", table, count, want, err)
	}
}
