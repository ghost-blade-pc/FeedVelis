package redis

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	cacheApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/observability"
	"github.com/ghost-blade-pc/Velis_Feed/backend/test/testkit/redistest"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	client "github.com/redis/go-redis/v9"
)

func testCache(t *testing.T, h *redistest.Harness, now func() time.Time, observer cacheApp.Observer) *Cache {
	t.Helper()
	cfg := config.Default()
	cfg.Cache.Enabled = true
	cfg.Cache.Namespace = h.Namespace
	cfg.Cache.Redis.Address = h.Address
	cfg.Cache.Redis.Username = config.SecretBytes(h.Client.Options().Username)
	cfg.Cache.Redis.Password = config.SecretBytes(h.Client.Options().Password)
	c, err := New(cfg.Cache, observer, now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
func testFragment(id int64) cacheApp.CardFragment {
	return cacheApp.CardFragment{Identity: cacheApp.CardIdentity{ArticleID: id, RevisionID: id}, Origin: articleDomain.OriginUser, Title: "标题", Excerpt: "摘录"}
}
func trackCards(t *testing.T, h *redistest.Harness, c *Cache, fragments ...cacheApp.CardFragment) {
	t.Helper()
	for _, f := range fragments {
		if err := h.Track(c.cardKey(f.Identity)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRedisCardsPartialCorruptionAndAbsoluteExpiry(t *testing.T) {
	h := redistest.New(t)
	now := time.Now().UTC()
	c := testCache(t, h, func() time.Time { return now }, nil)
	f1, f2 := testFragment(1), testFragment(2)
	trackCards(t, h, c, f1, f2)
	ctx := c.NewRequest(context.Background())
	if err := c.PutCards(ctx, []cacheApp.CardWrite{{Fragment: f1, CreatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	ids := []cacheApp.CardIdentity{f2.Identity, f1.Identity}
	v, err := c.GetCards(ctx, ids)
	if err != nil || len(v) != 1 || v[f1.Identity].Title != "标题" {
		t.Fatal(v, err)
	}
	before, err := h.Client.PTTL(context.Background(), c.cardKey(f1.Identity)).Result()
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	v, err = c.GetCards(c.NewRequest(context.Background()), ids)
	if err != nil || len(v) != 1 {
		t.Fatal(v, err)
	}
	after, _ := h.Client.PTTL(context.Background(), c.cardKey(f1.Identity)).Result()
	if after > before {
		t.Fatal("命中续期")
	}
	// 迟到旧回填剩余 4 分钟，下一次迟到到过期后不再写入。
	if err = c.PutCards(c.NewRequest(context.Background()), []cacheApp.CardWrite{{Fragment: f2, CreatedAt: now.Add(-time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	expires, _ := h.Client.PExpireTime(context.Background(), c.cardKey(f2.Identity)).Result()
	if expires.Milliseconds() != now.Add(4*time.Minute).UnixMilli() {
		t.Fatal("迟到回填延长寿命", expires)
	}
	now = now.Add(5 * time.Minute)
	if err = c.PutCards(c.NewRequest(context.Background()), []cacheApp.CardWrite{{Fragment: f2, CreatedAt: now.Add(-6 * time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	v, err = c.GetCards(c.NewRequest(context.Background()), ids)
	if err != nil || len(v) != 0 {
		t.Fatal("绝对过期未忽略", v, err)
	}
}

func TestRedisInvalidValuesAreMisses(t *testing.T) {
	h := redistest.New(t)
	c := testCache(t, h, nil, nil)
	f := testFragment(1)
	trackCards(t, h, c, f)
	key := c.cardKey(f.Identity)
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		mutate func(*envelope)
	}{
		{"unknown_version", func(e *envelope) { e.Version = 9; e.Checksum = checksum(*e) }},
		{"identity", func(e *envelope) { e.Identity = "wrong"; e.Checksum = checksum(*e) }},
		{"checksum", func(e *envelope) { e.Checksum = "bad" }},
		{"future", func(e *envelope) {
			e.CreatedAt = time.Now().Add(time.Hour)
			e.ExpiresAt = e.CreatedAt.Add(c.cfg.CardTTL)
			e.Checksum = checksum(*e)
		}},
		{"lifetime", func(e *envelope) { e.ExpiresAt = e.CreatedAt.Add(time.Hour); e.Checksum = checksum(*e) }},
		{"invalid_field", func(e *envelope) {
			bad := f
			bad.Identity.RevisionID = 0
			e.Payload, _ = json.Marshal(bad)
			e.Checksum = checksum(*e)
		}},
		{"unknown_field", func(e *envelope) { e.Payload = []byte(`{"private_body":"hidden"}`); e.Checksum = checksum(*e) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _, _ := encode(cardVersion, key, f, time.Now(), c.cfg.CardTTL, time.Now(), maxCardBytes)
			var e envelope
			_ = json.Unmarshal(raw, &e)
			tc.mutate(&e)
			raw, _ = json.Marshal(e)
			if err := h.Client.Set(ctx, key, raw, time.Minute).Err(); err != nil {
				t.Fatal(err)
			}
			v, err := c.GetCards(c.NewRequest(ctx), []cacheApp.CardIdentity{f.Identity})
			if err != nil || len(v) != 0 {
				t.Fatal(v, err)
			}
		})
	}
	for _, value := range []string{"{", strings.Repeat("x", maxCardBytes+1)} {
		if err := h.Client.Set(ctx, key, value, time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
		v, e := c.GetCards(c.NewRequest(ctx), []cacheApp.CardIdentity{f.Identity})
		if e != nil || len(v) != 0 {
			t.Fatal(v, e)
		}
	}
	_ = h.Client.Del(ctx, key).Err()
	if err := h.Client.HSet(ctx, key, "wrong", "type").Err(); err != nil {
		t.Fatal(err)
	}
	v, e := c.GetCards(c.NewRequest(ctx), []cacheApp.CardIdentity{f.Identity})
	if e != nil || len(v) != 0 {
		t.Fatal(v, e)
	}
}

func TestRedisLatestAndPlanIsolation(t *testing.T) {
	h := redistest.New(t)
	c := testCache(t, h, nil, nil)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for limit := 1; limit <= 50; limit++ {
		q := cacheApp.LatestQuery{Limit: limit}
		_ = h.Track(c.latestKey(q))
		if err := c.PutLatest(c.NewRequest(ctx), q, cacheApp.LatestPage{Exhausted: true}, now); err != nil {
			t.Fatal(err)
		}
	}
	q := cacheApp.LatestQuery{Limit: 20}
	if _, hit, e := c.GetLatest(c.NewRequest(ctx), q); e != nil || !hit {
		t.Fatal(hit, e)
	}
	q.Position = &articleDomain.Cursor{ArticleID: 100, SortAt: now}
	_ = h.Track(c.latestKey(q))
	page := cacheApp.LatestPage{Candidates: []cacheApp.Candidate{{ArticleID: 99, SortAt: now}}, Exhausted: true}
	if err := c.PutLatest(c.NewRequest(ctx), q, page, now); err != nil {
		t.Fatal(err)
	}
	other := q
	other.Limit = 10
	if _, hit, e := c.GetLatest(c.NewRequest(ctx), other); e != nil || hit {
		t.Fatal(hit, e)
	}
	if err := c.InvalidateLatest(c.NewRequest(ctx)); err != nil {
		t.Fatal(err)
	}
	for limit := 1; limit <= 50; limit++ {
		if _, hit, e := c.GetLatest(c.NewRequest(ctx), cacheApp.LatestQuery{Limit: limit}); e != nil || hit {
			t.Fatal("首页未失效", limit, hit, e)
		}
	}
	if _, hit, e := c.GetLatest(c.NewRequest(ctx), q); e != nil || !hit {
		t.Fatal("失效删除了续页", hit, e)
	}
	id := cacheApp.PlanIdentity{UserID: "c1000000-0000-0000-0000-000000000002", ProfileHash: strings.Repeat("a", 64), ConfigHash: strings.Repeat("b", 64)}
	_ = h.Track(c.planKey(id))
	plan := cacheApp.RecommendationPlan{Items: []recommendation.FrozenItem{{ID: 1, Reason: "keyword_match"}}, OriginalIDs: []int64{1, 2}, ExclusionHash: strings.Repeat("c", 64), RankingVersion: recommendation.RankingVersion}
	if err := c.PutPlan(c.NewRequest(ctx), id, plan, now); err != nil {
		t.Fatal(err)
	}
	if v, hit, e := c.GetPlan(c.NewRequest(ctx), id); e != nil || !hit || len(v.OriginalIDs) != 2 {
		t.Fatal(v, hit, e)
	}
	id.UserID = "c1000000-0000-0000-0000-000000000003"
	if _, hit, e := c.GetPlan(c.NewRequest(ctx), id); e != nil || hit {
		t.Fatal("跨用户计划命中", hit, e)
	}
}

func TestRedisBoundedBatchAndRecovery(t *testing.T) {
	h := redistest.New(t)
	c := testCache(t, h, nil, nil)
	c.cfg.BatchSize = 2
	ctx := context.Background()
	now := time.Now()
	var writes []cacheApp.CardWrite
	var ids []cacheApp.CardIdentity
	for i := int64(1); i <= 5; i++ {
		f := testFragment(i)
		trackCards(t, h, c, f)
		writes = append(writes, cacheApp.CardWrite{Fragment: f, CreatedAt: now})
		ids = append(ids, f.Identity)
	}
	if err := c.PutCards(c.NewRequest(ctx), writes); err != nil {
		t.Fatal(err)
	}
	v, e := c.GetCards(c.NewRequest(ctx), ids)
	if e != nil || len(v) != 5 {
		t.Fatal(v, e)
	}
	if v, e := c.GetCards(c.NewRequest(ctx), make([]cacheApp.CardIdentity, 501)); e != nil || len(v) != 0 {
		t.Fatal(v, e)
	}
	if err := h.Clear(); err != nil {
		t.Fatal(err)
	}
	v, e = c.GetCards(c.NewRequest(ctx), ids)
	if e != nil || len(v) != 0 {
		t.Fatal(v, e)
	}
	for _, w := range writes {
		_ = h.Track(c.cardKey(w.Fragment.Identity))
	}
	if err := c.PutCards(c.NewRequest(ctx), writes); err != nil {
		t.Fatal(err)
	}
	v, e = c.GetCards(c.NewRequest(ctx), ids)
	if e != nil || len(v) != 5 {
		t.Fatal(v, e)
	}
}

func TestRedisRequestBudgetFailureBypassAndCancellation(t *testing.T) {
	h := redistest.New(t)
	p := redistest.NewProxy(t, h.Address)
	metrics := observability.NewCacheMetrics(prometheus.NewRegistry(), nil)
	c := testCache(t, h, nil, metrics)
	_ = c.Close()
	cfg := c.cfg
	cfg.Redis.Address = p.Address()
	var err error
	c, err = New(cfg, metrics, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	f := testFragment(1)
	trackCards(t, h, c, f)
	ctx := context.Background()
	// 建立连接，避免把握手延迟混入操作预算断言。
	_, _ = c.GetCards(c.NewRequest(ctx), []cacheApp.CardIdentity{f.Identity})
	p.Delay(200 * time.Millisecond)
	request := c.NewRequest(ctx)
	start := time.Now()
	v, e := c.GetCards(request, []cacheApp.CardIdentity{f.Identity})
	elapsed := time.Since(start)
	if e != nil || len(v) != 0 || elapsed > 150*time.Millisecond {
		t.Fatal(v, e, elapsed)
	}
	start = time.Now()
	if err := c.PutCards(request, []cacheApp.CardWrite{{Fragment: f, CreatedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 20*time.Millisecond {
		t.Fatal("故障后的回填没有直接绕过")
	}
	if testutil.ToFloat64(metrics.Failures.WithLabelValues("card", "get", "bypass", "timeout")) != 1 || testutil.ToFloat64(metrics.Failures.WithLabelValues("card", "set", "failure", "budget")) != 1 {
		t.Fatal("超时或回填失败分类错误")
	}
	p.Delay(0)
	p.Disconnect(true)
	if err := c.InvalidateLatest(c.NewRequest(ctx)); err != nil {
		t.Fatal("失效失败影响业务返回", err)
	}
	if testutil.ToFloat64(metrics.Failures.WithLabelValues("latest", "invalidate", "failure", "transport")) != 1 {
		t.Fatal("失效失败分类错误")
	}
	v, e = c.GetCards(c.NewRequest(ctx), []cacheApp.CardIdentity{f.Identity})
	if e != nil || len(v) != 0 {
		t.Fatal(v, e)
	}
	p.Disconnect(false)
	if err := c.PutCards(c.NewRequest(ctx), []cacheApp.CardWrite{{Fragment: f, CreatedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	v, e = c.GetCards(c.NewRequest(ctx), []cacheApp.CardIdentity{f.Identity})
	if e != nil || len(v) != 1 {
		t.Fatal("恢复未回填", v, e)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.GetCards(c.NewRequest(canceled), []cacheApp.CardIdentity{f.Identity}); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestRedisMetricsPartialHitAndSharedBatchBudget(t *testing.T) {
	h := redistest.New(t)
	now := time.Now()
	metrics := observability.NewCacheMetrics(prometheus.NewRegistry(), nil)
	c := testCache(t, h, func() time.Time { return now }, metrics)
	f1, f2, f3 := testFragment(1), testFragment(2), testFragment(3)
	trackCards(t, h, c, f1, f2, f3)
	if err := c.PutCards(c.NewRequest(context.Background()), []cacheApp.CardWrite{{Fragment: f1, CreatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	if err := h.Client.Set(context.Background(), c.cardKey(f3.Identity), "broken", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	_, err := c.GetCards(c.NewRequest(context.Background()), []cacheApp.CardIdentity{f1.Identity, f2.Identity, f3.Identity})
	if err != nil {
		t.Fatal(err)
	}
	if testutil.ToFloat64(metrics.Entries.WithLabelValues("card", "hit")) != 1 || testutil.ToFloat64(metrics.Entries.WithLabelValues("card", "miss")) != 2 || testutil.ToFloat64(metrics.Entries.WithLabelValues("card", "corrupt")) != 1 {
		t.Fatal("适配器部分命中指标错误")
	}
	// 控制时钟在每次 Redis 调用结束时前进；四个单项批次共享 100ms。
	c.cfg.BatchSize = 1
	calls := 0
	c.client.AddHook(elapsedHook{advance: func() {
		calls++
		d := 40 * time.Millisecond
		if calls == 3 {
			d = 20 * time.Millisecond
		}
		now = now.Add(d)
	}})
	request := c.NewRequest(context.Background())
	_, err = c.GetCards(request, []cacheApp.CardIdentity{f1.Identity, f2.Identity, f3.Identity, f1.Identity})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || cacheApp.BudgetFrom(request).Remaining() != 0 || testutil.ToFloat64(metrics.Failures.WithLabelValues("card", "get", "bypass", "budget")) < 1 {
		t.Fatal("批次重置了请求额度")
	}
	if c.client.Options().MaxRetries != 0 || c.client.Options().DialerRetries != -1 || !c.client.Options().ContextTimeoutEnabled {
		t.Fatal("SDK 重试或 context 选项错误")
	}
}

type elapsedHook struct{ advance func() }

func (h elapsedHook) DialHook(next client.DialHook) client.DialHook { return next }
func (h elapsedHook) ProcessHook(next client.ProcessHook) client.ProcessHook {
	return func(ctx context.Context, cmd client.Cmder) error {
		err := next(ctx, cmd)
		if cmd.Name() == "eval" {
			h.advance()
		}
		return err
	}
}
func (h elapsedHook) ProcessPipelineHook(next client.ProcessPipelineHook) client.ProcessPipelineHook {
	return next
}

func TestRedisLatestPositionVersionAndAbsoluteTTL(t *testing.T) {
	h := redistest.New(t)
	c := testCache(t, h, nil, nil)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	q := cacheApp.LatestQuery{Limit: 2, Position: &articleDomain.Cursor{ArticleID: 9, SortAt: now}}
	same := q
	same.Position = &articleDomain.Cursor{ArticleID: 9, SortAt: now.In(time.FixedZone("UTC+8", 8*3600))}
	different := q
	different.Limit = 3
	if c.latestKey(q) != c.latestKey(same) || c.latestKey(q) == c.latestKey(different) {
		t.Fatal("位置没有规范化或 limit 混用")
	}
	key := c.latestKey(q)
	if err := h.Track(key); err != nil {
		t.Fatal(err)
	}
	page := cacheApp.LatestPage{Candidates: []cacheApp.Candidate{{ArticleID: 8, SortAt: now}}, Exhausted: true}
	created := now.Add(-2 * time.Second)
	if err := c.PutLatest(c.NewRequest(ctx), q, page, created); err != nil {
		t.Fatal(err)
	}
	before := h.Client.PTTL(ctx, key).Val()
	for i := 0; i < 3; i++ {
		if _, hit, err := c.GetLatest(c.NewRequest(ctx), same); err != nil || !hit {
			t.Fatal(hit, err)
		}
	}
	if after := h.Client.PTTL(ctx, key).Val(); after > before || after > 3*time.Second {
		t.Fatal("热点页延长绝对 TTL", before, after)
	}
	raw, err := h.Client.Get(ctx, key).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	var e envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	e.Version = 99
	e.Checksum = checksum(e)
	raw, err = json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Client.Set(ctx, key, raw, time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	if _, hit, err := c.GetLatest(c.NewRequest(ctx), q); err != nil || hit {
		t.Fatal("未知 latest 版本被命中", hit, err)
	}
}
