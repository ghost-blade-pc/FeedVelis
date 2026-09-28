package integration

import (
	"context"
	"testing"
	"time"

	feedbackApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlefeedback"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

type profileClock struct{ now time.Time }

func (c profileClock) Now() time.Time { return c.now }

func TestArticleFeedbackProfileExpirationAndVisibility(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedFeedbackArticle(t, env)
	ctx := context.Background()
	const user = "72000000-0000-0000-0000-000000000001"
	r := postgres.NewArticleFeedbackRepository(env.pool)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	if err := r.RecordRead(ctx, user, 7201, now.Add(-91*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := r.SetNotInterested(ctx, user, 7201, true, now.Add(-181*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	profile, err := feedbackApp.NewProfileService(r, profileClock{now}).Profile(ctx, user, []int64{7201})
	if err != nil || len(profile.Excluded) != 0 || len(profile.Topics) != 0 {
		t.Fatalf("清理停机时到期事实仍参与画像: %+v %v", profile, err)
	}
	samples, err := r.Samples(ctx, user, now)
	if err != nil || len(samples) != 0 {
		t.Fatalf("过期样本未过滤: %+v %v", samples, err)
	}
	if err := r.RecordRead(ctx, user, 7201, now); err != nil {
		t.Fatal(err)
	}
	if err := r.SetNotInterested(ctx, user, 7201, true, now); err != nil {
		t.Fatal(err)
	}
	profile, err = feedbackApp.NewProfileService(r, profileClock{now}).Profile(ctx, user, []int64{7201})
	if err != nil || profile.Excluded[7201] != "not_interested_article" {
		t.Fatalf("有效排除缺失: %+v %v", profile, err)
	}
	const otherUser = "72000000-0000-0000-0000-000000000002"
	if _, err := env.pool.Exec(ctx, `INSERT INTO velis.users(id,username,nickname,password_hash,role,status,created_at,updated_at)
VALUES($1,'feedback_profile_b','用户B','hash','user','active',$2,$2)`, otherUser, now); err != nil {
		t.Fatal(err)
	}
	other, err := feedbackApp.NewProfileService(r, profileClock{now}).Profile(ctx, otherUser, []int64{7201})
	if err != nil || len(other.Excluded) != 0 || len(other.Topics) != 0 || len(other.Sources) != 0 {
		t.Fatalf("画像跨用户泄漏: %+v %v", other, err)
	}
	_, err = env.pool.Exec(ctx, `INSERT INTO velis.ai_generation_results
(id,article_id,revision_id,provider,model,profile_version,workflow_version,prompt_version,generation_input_hash,input_truncated,summary,keywords,topics,generated_at)
VALUES('72000000-0000-0000-0000-000000000011',7201,7211,'stub','chat','g-v1','w-v1','p-v1',repeat('a',64),false,'摘要',ARRAY['关键词'],ARRAY['旧主题'],$1)`, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.pool.Exec(ctx, `INSERT INTO velis.ai_current_selections(article_id,revision_id,generation_result_id,generation_profile_version,updated_at)
VALUES(7201,7211,'72000000-0000-0000-0000-000000000011','g-v1',$1)`, now)
	if err != nil {
		t.Fatal(err)
	}
	profile, err = feedbackApp.NewProfileService(r, profileClock{now}).Profile(ctx, user, []int64{7201})
	if err != nil || profile.Topics["旧主题"].NegativeArticles != 1 || profile.Topics["旧主题"].Weight != -1 {
		t.Fatalf("主题负证据错误: %+v %v", profile, err)
	}
	_, err = env.pool.Exec(ctx, `INSERT INTO velis.article_versions(id,article_id,revision_no,title,plain_text,excerpt,language,content_hash,sanitizer_version,created_at)
OVERRIDING SYSTEM VALUE VALUES(7212,7201,2,'修改后','正文','正文','zh-CN',repeat('b',64),1,$1)`, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.pool.Exec(ctx, `UPDATE velis.articles SET current_revision_id=7212 WHERE id=7201`)
	if err != nil {
		t.Fatal(err)
	}
	profile, err = feedbackApp.NewProfileService(r, profileClock{now}).Profile(ctx, user, []int64{7201})
	if err != nil || len(profile.Topics) != 0 || profile.Excluded[7201] != "not_interested_article" {
		t.Fatalf("修订后仍引用旧主题: %+v %v", profile, err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE velis.articles SET status='offline',offline_reason='author',offline_at=$2 WHERE id=$1`, 7201, now); err != nil {
		t.Fatal(err)
	}
	profile, err = feedbackApp.NewProfileService(r, profileClock{now}).Profile(ctx, user, []int64{7201})
	if err != nil || len(profile.Excluded) != 0 {
		t.Fatalf("下架仍进入画像: %+v %v", profile, err)
	}
	if _, err := r.CleanupExpired(ctx, now, 1); err != nil {
		t.Fatal(err)
	}
	assertFeedbackCount(t, env, "article_read_windows", 1)
	assertFeedbackCount(t, env, "article_not_interested", 1)
}

func TestArticleFeedbackEndToEndReadCapAndCancellation(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedFeedbackArticle(t, env)
	ctx := context.Background()
	const user = "72000000-0000-0000-0000-000000000001"
	r := postgres.NewArticleFeedbackRepository(env.pool)
	now := time.Date(2026, 9, 28, 23, 40, 0, 0, time.UTC)
	_, err := env.pool.Exec(ctx, `INSERT INTO velis.ai_generation_results
(id,article_id,revision_id,provider,model,profile_version,workflow_version,prompt_version,generation_input_hash,input_truncated,summary,keywords,topics,generated_at)
VALUES('72000000-0000-0000-0000-000000000012',7201,7211,'stub','chat','g-v1','w-v1','p-v1',repeat('a',64),false,'摘要',ARRAY['关键词'],ARRAY['主题'],$1)`, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.pool.Exec(ctx, `INSERT INTO velis.ai_current_selections(article_id,revision_id,generation_result_id,generation_profile_version,updated_at)
VALUES(7201,7211,'72000000-0000-0000-0000-000000000012','g-v1',$1)`, now)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		at := now.AddDate(0, 0, -i)
		if err := r.RecordRead(ctx, user, 7201, at); err != nil {
			t.Fatal(err)
		}
		if err := r.RecordRead(ctx, user, 7201, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.RecordRead(ctx, user, 7201, now.Add(31*time.Minute)); err != nil {
		t.Fatal(err)
	}
	profile, err := feedbackApp.NewProfileService(r, profileClock{now.Add(32 * time.Minute)}).Profile(ctx, user, []int64{7201})
	if err != nil || profile.Topics["主题"].Weight != 3 {
		t.Fatalf("阅读权重未封顶: %+v %v", profile, err)
	}
	if err := r.SetFavorite(ctx, user, 7201, true, now); err != nil {
		t.Fatal(err)
	}
	profile, err = feedbackApp.NewProfileService(r, profileClock{now.Add(32 * time.Minute)}).Profile(ctx, user, []int64{7201})
	if err != nil || profile.Topics["主题"].Weight != 6 {
		t.Fatalf("收藏贡献错误: %+v %v", profile, err)
	}
	if err := r.SetNotInterested(ctx, user, 7201, true, now); err != nil {
		t.Fatal(err)
	}
	profile, err = feedbackApp.NewProfileService(r, profileClock{now.Add(32 * time.Minute)}).Profile(ctx, user, []int64{7201})
	if err != nil || profile.Excluded[7201] != "not_interested_article" || profile.Topics["主题"].Weight != -1 {
		t.Fatalf("负反馈优先失败: %+v %v", profile, err)
	}
	if err := r.SetNotInterested(ctx, user, 7201, false, now); err != nil {
		t.Fatal(err)
	}
	if err := r.SetFavorite(ctx, user, 7201, false, now); err != nil {
		t.Fatal(err)
	}
	profile, err = feedbackApp.NewProfileService(r, profileClock{now.Add(32 * time.Minute)}).Profile(ctx, user, []int64{7201})
	if err != nil || len(profile.Excluded) != 0 || profile.Topics["主题"].Weight != 3 {
		t.Fatalf("撤销后画像未恢复: %+v %v", profile, err)
	}
}
