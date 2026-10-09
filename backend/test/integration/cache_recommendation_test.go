package integration

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	feedbackApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlefeedback"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleread"
	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

type fixedPlanIndex struct {
	ids   []int64
	calls int
}

func (i *fixedPlanIndex) Recall(context.Context, recommendation.Terms, int, time.Duration) ([]searchApp.Candidate, error) {
	i.calls++
	items := make([]searchApp.Candidate, len(i.ids))
	for n, id := range i.ids {
		items[n] = searchApp.Candidate{ArticleID: id}
	}
	return items, nil
}
func TestCacheReadRecommendationPlanIsolationExclusionsAndContinuation(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Hour)
	const author = "c5000000-0000-0000-0000-000000000001"
	users := []string{"c5000000-0000-0000-0000-000000000002", "c5000000-0000-0000-0000-000000000003"}
	seedIntegrationUser(t, env, author, "plan_author", "user", now)
	for n, u := range users {
		seedIntegrationUser(t, env, u, []string{"plan_a", "plan_b"}[n], "user", now)
	}
	ids := []int64{}
	for _, hash := range []string{"a", "b", "c", "d"} {
		ids = append(ids, createPublishedArticle(t, env, author, hash, now))
	}
	seedRecommendationLabels(t, env, ids[0], "Go", now)
	feedback := postgres.NewArticleFeedbackRepository(env.pool)
	for _, u := range users {
		if err := feedback.SetFavorite(ctx, u, ids[0], true, now); err != nil {
			t.Fatal(err)
		}
	}
	cache, h := newOwnedReadCache(t, "")
	repo := postgres.NewArticleRepository(env.pool)
	tx := postgres.NewTxManager(env.pool)
	reader := articleread.NewService(repo, tx, cache, nil, 100, nil)
	profile := feedbackApp.NewProfileService(feedback, profileClock{time.Now().UTC()})
	codec, _ := recommendation.NewCursorCodec([]byte(strings.Repeat("r", 32)))
	index := &fixedPlanIndex{ids: ids}
	service := recommendation.NewService(profile, index, reader, codec, nil, recommendation.Config{FirstQueryTimeout: time.Second, BM25Candidates: 100, KNNCandidates: 100, CursorTTL: 2 * time.Minute}, time.Now).WithPlanCache(cache)
	get := func(user string) recommendation.Page {
		t.Helper()
		p, e := service.Get(ctx, user, 1, "")
		if e != nil || p.NextCursor == nil {
			t.Fatal(p, e)
		}
		return p
	}
	first := get(users[0])
	again := get(users[0])
	get(users[1])
	if index.calls != 2 || !reflect.DeepEqual(first.Items, again.Items) {
		t.Fatal("真实计划未隔离或未命中", index.calls)
	}
	oldState, e := codec.Decode(*first.NextCursor, users[0], time.Now())
	if e != nil {
		t.Fatal(e)
	}
	newState, e := codec.Decode(*again.NextCursor, users[0], time.Now())
	if e != nil || !newState.StartedAt.After(oldState.StartedAt) || newState.Offset != oldState.Offset {
		t.Fatal("首查时间/状态复用", newState, e)
	}
	planKeys := 0
	for _, key := range h.Keys() {
		if strings.Contains(key, ":recommend:") {
			planKeys++
			if ttl := h.Client.PTTL(ctx, key).Val(); ttl <= 0 || ttl > 30*time.Second {
				t.Fatal("计划非绝对30秒寿命", ttl)
			}
		}
	}
	if planKeys != 2 {
		t.Fatal("同画像两名用户键未隔离", planKeys)
	}
	// 无标签的用户文章不会改变画像；原候选签名必须检测硬排除变化。
	before, e := profile.Profile(ctx, users[0], nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = feedback.SetNotInterested(ctx, users[0], ids[1], true, time.Now()); e != nil {
		t.Fatal(e)
	}
	get(users[0])
	if index.calls != 3 {
		t.Fatal("新硬排除未重算", index.calls)
	}
	after, e := profile.Profile(ctx, users[0], nil)
	if e != nil || recommendation.ProfileFingerprint(before) != recommendation.ProfileFingerprint(after) {
		t.Fatal("测试无标签排除改变画像", e)
	}
	if e = feedback.SetNotInterested(ctx, users[0], ids[1], false, time.Now()); e != nil {
		t.Fatal(e)
	}
	get(users[0])
	if index.calls != 4 {
		t.Fatal("撤销硬排除未重算", index.calls)
	}
	if e = feedback.SetNotInterested(ctx, users[0], ids[1], true, time.Now()); e != nil {
		t.Fatal(e)
	}
	get(users[0])
	if _, e = env.pool.Exec(ctx, `UPDATE velis.article_not_interested SET created_at=$1::timestamptz-interval '30 days',expires_at=$1::timestamptz WHERE user_id=$2 AND article_id=$3`, time.Now().Add(-time.Second), users[0], ids[1]); e != nil {
		t.Fatal(e)
	}
	get(users[0])
	if index.calls != 6 {
		t.Fatal("自然过期未重算", index.calls)
	}
	// 清空所有本次键后续页仅使用客户端冻结状态；新排除立即生效。
	if e = feedback.SetNotInterested(ctx, users[0], ids[1], true, time.Now()); e != nil {
		t.Fatal(e)
	}
	if e = h.Clear(); e != nil {
		t.Fatal(e)
	}
	p, e := service.Get(ctx, users[0], 2, *first.NextCursor)
	if e != nil || index.calls != 6 {
		t.Fatal("续页重新召回", p, e, index.calls)
	}
	for _, item := range p.Items {
		if item.Article.ID == ids[1] {
			t.Fatal("续页泄露新硬排除")
		}
	}
	// 专用 latest 保留本人排除、skip、StartedAt 和位置；不使用公共 latest 页。
	start := time.Now().UTC()
	if _, e = env.pool.Exec(ctx, `UPDATE velis.articles SET published_at=$1 WHERE id=$2`, start.Add(time.Hour), ids[3]); e != nil {
		t.Fatal(e)
	}
	original, e := repo.ListRecommendationLatest(ctx, nil, 10, []int64{ids[0]}, users[0], start, start)
	if e != nil {
		t.Fatal(e)
	}
	actual, e := reader.ListRecommendationLatest(ctx, nil, 10, []int64{ids[0]}, users[0], start, start)
	if e != nil || !reflect.DeepEqual(original, actual) || len(actual) != 1 || actual[0].ID != ids[2] {
		t.Fatal("专用latest契约变化", actual, original, e)
	}
	cursor := &articleDomain.Cursor{ArticleID: actual[0].ID, SortAt: actual[0].SortAt}
	tail, e := reader.ListRecommendationLatest(ctx, cursor, 10, nil, "", start, start)
	baseline, e2 := repo.ListRecommendationLatest(ctx, cursor, 10, nil, "", start, start)
	if e != nil || e2 != nil || !reflect.DeepEqual(tail, baseline) {
		t.Fatal("专用latest位置变化", tail, baseline, e, e2)
	}
	for _, key := range h.Keys() {
		if strings.Contains(key, ":latest:") {
			t.Fatal("专用latest复用公共页")
		}
	}
}
