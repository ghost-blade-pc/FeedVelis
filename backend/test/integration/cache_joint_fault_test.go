package integration

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	feedbackApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlefeedback"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleread"
	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/observability"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"github.com/ghost-blade-pc/Velis_Feed/backend/test/testkit/redistest"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// disconnectFillCache 在真实 Redis 读取完成、快照提交之后才断连，验证回填失败。
type disconnectFillCache struct {
	articlecache.Cache
	proxy *redistest.Proxy
	fail  bool
}

func (c *disconnectFillCache) PutCards(ctx context.Context, writes []articlecache.CardWrite) error {
	if c.fail {
		c.proxy.Disconnect(true)
	}
	return c.Cache.PutCards(ctx, writes)
}

func TestCacheReadJointFaultMatrixAndRealPostgresFailure(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Hour)
	const author = "c6000000-0000-0000-0000-000000000001"
	const user = "c6000000-0000-0000-0000-000000000002"
	seedIntegrationUser(t, env, author, "joint_author", "user", now)
	seedIntegrationUser(t, env, user, "joint_reader", "user", now)
	ids := []int64{}
	for _, hash := range []string{"a", "b", "c", "d", "e"} {
		id := createPublishedArticle(t, env, author, hash, now)
		ids = append(ids, id)
		seedRecommendationLabels(t, env, id, "Go", now)
	}
	stack := newProjectionE2E(t, env, 3)
	stack.drain(t, ctx)
	if e := stack.client.Refresh(ctx, stack.index); e != nil {
		t.Fatal(e)
	}
	h0 := redistest.New(t)
	proxy := redistest.NewProxy(t, h0.Address)
	reg := prometheus.NewRegistry()
	metrics := observability.NewCacheMetrics(reg, nil)
	cache, h := newOwnedReadCache(t, proxy.Address(), metrics)
	repo := postgres.NewArticleRepository(env.pool)
	tx := postgres.NewTxManager(env.pool)
	shared := articleread.NewService(repo, tx, cache, metrics, 100, nil)
	latest := articleApp.NewService(repo, nil, nil).WithLatestReader(shared)
	sc, _ := searchApp.NewCursorCodec([]byte(strings.Repeat("s", 32)))
	search := searchApp.NewService(stack.client, shared, sc, nil, searchApp.Config{Timeout: time.Second, PITKeepAlive: time.Minute, CandidateBatchSize: 100, MaxCandidatesPerRequest: 500})
	feedback := postgres.NewArticleFeedbackRepository(env.pool)
	for _, id := range []int64{ids[0], ids[4]} {
		if e := feedback.SetFavorite(ctx, user, id, true, now); e != nil {
			t.Fatal(e)
		}
	}
	profile := feedbackApp.NewProfileService(feedback, profileClock{time.Now().UTC()})
	rc, _ := recommendation.NewCursorCodec([]byte(strings.Repeat("r", 32)))
	cfg := recommendation.Config{FirstQueryTimeout: time.Second, BM25Candidates: 100, KNNCandidates: 100, CursorTTL: 2 * time.Minute}
	makeRec := func(reader recommendation.Reader) *recommendation.Service {
		return recommendation.NewService(profile, stack.client, reader, rc, nil, cfg, time.Now).WithPlanCache(cache, func(ctx context.Context) { metrics.Fallback(ctx, articlecache.Recommend, 1) })
	}
	rec := makeRec(shared)
	readAll := func() (articleApp.Page, searchApp.Page, recommendation.Page) {
		t.Helper()
		l, e := latest.List(ctx, "", 2)
		if e != nil {
			t.Fatal(e)
		}
		s, e := search.Search(ctx, searchApp.Request{Q: "投影文章", Limit: 2})
		if e != nil {
			t.Fatal(e)
		}
		if s.NextCursor != nil {
			closeLoadBM(t, sc, stack)(*s.NextCursor)
		}
		r, e := rec.Get(ctx, user, 2, "")
		if e != nil {
			t.Fatal(e)
		}
		return l, s, r
	}
	expectedL, expectedS, expectedR := readAll()
	readAll()
	// 清空本次全部三对象，已有游标仍可续页且不以 Redis 保存消费状态。
	if e := h.Clear(); e != nil {
		t.Fatal(e)
	}
	cont, e := rec.Get(ctx, user, 2, *expectedR.NextCursor)
	if e != nil || len(cont.Items) != 2 {
		t.Fatal("清空后续页失效", cont, e)
	}
	readAll()
	readAll()
	for _, object := range []string{"card", "latest", "recommend"} {
		if testutil.ToFloat64(metrics.Entries.WithLabelValues(object, "hit")) == 0 {
			t.Fatal("没有恢复命中", object)
		}
	}
	for _, mode := range []string{"wrong_type", "corrupt", "disconnect", "slow", "recovery"} {
		t.Run(mode, func(t *testing.T) {
			proxy.Disconnect(false)
			proxy.Delay(0)
			if mode == "wrong_type" || mode == "corrupt" {
				for _, key := range h.Keys() {
					if mode == "wrong_type" {
						if e := h.Client.Del(ctx, key).Err(); e != nil {
							t.Fatal(e)
						}
						if e := h.Client.LPush(ctx, key, "错误类型").Err(); e != nil {
							t.Fatal(e)
						}
					} else if e := h.Client.Set(ctx, key, "损坏", time.Minute).Err(); e != nil {
						t.Fatal(e)
					}
				}
			}
			if mode == "disconnect" {
				proxy.Disconnect(true)
			}
			if mode == "slow" {
				proxy.Delay(200 * time.Millisecond)
			}
			l, s, r := readAll()
			if !reflect.DeepEqual(l.Items, expectedL.Items) || !reflect.DeepEqual(s.Items, expectedS.Items) || !reflect.DeepEqual(r.Items, expectedR.Items) || r.Mode != expectedR.Mode || r.Degraded != expectedR.Degraded {
				t.Fatal("缓存故障改变卡片/排序/推荐降级")
			}
		})
	}
	proxy.Delay(0)
	proxy.Disconnect(false)
	// 暖缓存后更新修订、切换 AI 选择、下架和反馈；Redis 故障期间仍要复核真实当前事实。
	readAll()
	if _, _, e := repo.UpdateUserRevision(ctx, ids[0], author, 1, articleDomain.RevisionData{Title: "新修订", PlainText: "新正文", Excerpt: "新摘录", Language: "zh-CN", ContentHash: strings.Repeat("f", 64), SanitizerVersion: 1}, time.Now()); e != nil {
		t.Fatal(e)
	}
	factsForAI, e := repo.ListPublicFacts(ctx, []int64{ids[1]})
	if e != nil {
		t.Fatal(e)
	}
	nextGeneration := "c6000000-0000-0000-0000-000000000011"
	insertGenerationResult(t, env, nextGeneration, "p2", ids[1], factsForAI[0].Identity.RevisionID)
	setCurrentSelection(t, env, ids[1], factsForAI[0].Identity.RevisionID, &nextGeneration, nil)
	if _, e := env.pool.Exec(ctx, `UPDATE velis.ai_generation_results SET summary='切换后的摘要' WHERE id=(SELECT generation_result_id FROM velis.ai_current_selections WHERE article_id=$1)`, ids[1]); e != nil {
		t.Fatal(e)
	}
	if _, e := env.pool.Exec(ctx, `UPDATE velis.articles SET status='offline',offline_reason='author',offline_at=now() WHERE id=$1`, ids[2]); e != nil {
		t.Fatal(e)
	}
	if e := feedback.SetNotInterested(ctx, user, ids[3], true, time.Now()); e != nil {
		t.Fatal(e)
	}
	for _, fault := range []bool{false, true} {
		proxy.Disconnect(fault)
		items, e := shared.ListPublishedWithIdentity(ctx, ids)
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range items {
			switch v.Item.ID {
			case ids[0]:
				if v.Item.Title != "新修订" || v.Item.Enhancement != nil {
					t.Fatal("旧修订/AI泄露", v)
				}
			case ids[1]:
				if v.Item.Enhancement == nil || v.Item.Enhancement.Summary != "切换后的摘要" {
					t.Fatal("旧AI泄露", v)
				}
			case ids[2]:
				t.Fatal("下架泄露")
			}
		}
		_, _, r := readAll()
		for _, v := range r.Items {
			if v.Article.ID == ids[2] || v.Article.ID == ids[3] {
				t.Fatal("推荐泄露下架/新反馈")
			}
		}
	}
	proxy.Disconnect(false)
	// 仅在本进程代理上断开 PostgreSQL；共享库继续服务，暖 Redis 不得掩盖真实连接失败。
	pc, e := pgxpool.ParseConfig(env.databaseURL)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasSuffix(pc.ConnConfig.Database, "_test") {
		t.Fatal("非测试库")
	}
	pgProxy := redistest.NewProxy(t, net.JoinHostPort(pc.ConnConfig.Host, strconv.Itoa(int(pc.ConnConfig.Port))))
	host, port, e := net.SplitHostPort(pgProxy.Address())
	if e != nil {
		t.Fatal(e)
	}
	n, e := strconv.Atoi(port)
	if e != nil {
		t.Fatal(e)
	}
	pc.ConnConfig.Host = host
	pc.ConnConfig.Port = uint16(n)
	pc.ConnConfig.ConnectTimeout = 100 * time.Millisecond
	pc.ConnConfig.Fallbacks = nil
	brokenPool, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(brokenPool.Close)
	(&testEnv{pool: brokenPool}).requireTestDatabase(t)
	badShared := articleread.NewService(postgres.NewArticleRepository(brokenPool), postgres.NewTxManager(brokenPool), cache, metrics, 100, nil)
	if _, e = badShared.ListPublishedWithIdentity(ctx, ids); e != nil {
		t.Fatal(e)
	}
	readAll()
	pgProxy.Disconnect(true)
	faultCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if items, e := badShared.ListPublishedWithIdentity(faultCtx, ids); e == nil || len(items) != 0 {
		t.Fatal("暖缓存掩盖真实PG事实失败", items, e)
	}
	if _, _, _, e = badShared.ListLatest(faultCtx, nil, 2); e == nil {
		t.Fatal("暖latest掩盖PG故障")
	}
	badSearch := searchApp.NewService(stack.client, badShared, sc, nil, searchApp.Config{Timeout: time.Second, PITKeepAlive: time.Minute, CandidateBatchSize: 100, MaxCandidatesPerRequest: 500})
	if _, e = badSearch.Search(faultCtx, searchApp.Request{Q: "投影文章", Limit: 2}); searchApp.CodeOf(e) != searchApp.CodeDependencyUnavailable {
		t.Fatal("搜索掩盖PG故障", e)
	}
	planHitsBefore := testutil.ToFloat64(metrics.Entries.WithLabelValues("recommend", "hit"))
	if _, e = makeRec(badShared).Get(faultCtx, user, 2, ""); !errors.Is(e, recommendation.ErrDependencyUnavailable) {
		t.Fatal("暖计划掩盖PG事实失败", e)
	}
	if testutil.ToFloat64(metrics.Entries.WithLabelValues("recommend", "hit")) <= planHitsBefore {
		t.Fatal("真实PG故障用例未命中暖推荐计划")
	}
	brokenProfile := feedbackApp.NewProfileService(postgres.NewArticleFeedbackRepository(brokenPool), profileClock{time.Now().UTC()})
	if _, err := recommendation.NewService(brokenProfile, stack.client, shared, rc, nil, cfg, time.Now).WithPlanCache(cache).Get(faultCtx, user, 2, ""); !errors.Is(err, recommendation.ErrDependencyUnavailable) {
		t.Fatal("暖计划掩盖真实画像PG失败", err)
	}
	pgProxy.Disconnect(false)
	// 成功事实/片段回源后，真实 Redis SET 失败不能改变读取；指标必须记录。
	if e = h.Clear(); e != nil {
		t.Fatal(e)
	}
	fillFailuresBefore := testutil.ToFloat64(metrics.Failures.WithLabelValues("card", "set", "failure", "transport"))
	fillCache := &disconnectFillCache{cache, proxy, true}
	fillReader := articleread.NewService(repo, tx, fillCache, metrics, 100, nil)
	if items, e := fillReader.ListPublishedByIDs(ctx, []int64{ids[0]}); e != nil || len(items) != 1 || items[0].Title != "新修订" {
		t.Fatal("回填失败改变成功读取", items, e)
	}
	if testutil.ToFloat64(metrics.Failures.WithLabelValues("card", "set", "failure", "transport")) <= fillFailuresBefore {
		t.Fatal("真实回填传输失败未记录指标")
	}
	failuresBefore := testutil.ToFloat64(metrics.Failures.WithLabelValues("latest", "invalidate", "failure", "transport"))
	// 已提交事务的回调故障只影响指标，业务和同事务写入必须成功。
	invalidator := articleread.NewInvalidator(tx, cache, ctx, 100*time.Millisecond)
	e = tx.WithinTransaction(ctx, func(write context.Context) error {
		if _, e := repo.SetArticleState(write, ids[4], 1, articleDomain.StatusOffline, pointerOfflineReason(articleDomain.OfflineByAuthor), nil, nil, nil, time.Now()); e != nil {
			return e
		}
		return invalidator.ScheduleLatestInvalidation(write)
	})
	if e != nil {
		t.Fatal("真实失效失败改变提交", e)
	}
	facts, e := repo.ListPublicFacts(ctx, []int64{ids[4]})
	if e != nil || len(facts) != 0 {
		t.Fatal("业务未提交", facts, e)
	}
	if testutil.ToFloat64(metrics.Failures.WithLabelValues("latest", "invalidate", "failure", "transport")) <= failuresBefore {
		t.Fatal("失效传输失败无指标")
	}
	proxy.Disconnect(false)
	// 真实代理分段延迟，检验计划/卡片跨操作共享累计预算，而非仅单次超时。
	if e = h.Clear(); e != nil {
		t.Fatal(e)
	}
	_, _, _ = cache.GetLatest(cache.NewRequest(ctx), articlecache.LatestQuery{Limit: 2})
	proxy.Delay(35 * time.Millisecond)
	budgetCtx := cache.NewRequest(ctx)
	identity := recommendation.PlanIdentity{UserID: user, ProfileHash: strings.Repeat("a", 64), ConfigHash: strings.Repeat("b", 64)}
	started := time.Now()
	_, _, _ = cache.GetPlan(budgetCtx, identity)
	_, _, _ = cache.GetLatest(budgetCtx, articlecache.LatestQuery{Limit: 2})
	_, _ = cache.GetCards(budgetCtx, []articlecache.CardIdentity{{ArticleID: ids[0], RevisionID: 1}})
	elapsed := time.Since(started)
	budget := articlecache.BudgetFrom(budgetCtx)
	if elapsed > 180*time.Millisecond || 100*time.Millisecond-budget.Remaining() < 90*time.Millisecond {
		t.Fatal("预算按对象重置", elapsed, 100*time.Millisecond-budget.Remaining())
	}
	t.Logf("联合预算实测: wall=%s spent=%s；仅本次代理故障，三对象恢复成功", elapsed, 100*time.Millisecond-budget.Remaining())
	proxy.Delay(0)
}
