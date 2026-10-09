package integration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
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
	"github.com/prometheus/client_golang/prometheus"
)

type measuredReads struct {
	calls, rows, bytes [3]atomic.Int64
	snapshots, viewNS  atomic.Int64
}
type measuredReadRepository struct {
	*postgres.ArticleRepository
	counts *measuredReads
}

func (r measuredReadRepository) record(kind int, n int, value any) {
	raw, _ := json.Marshal(value)
	r.counts.calls[kind].Add(1)
	r.counts.rows[kind].Add(int64(n))
	r.counts.bytes[kind].Add(int64(len(raw)))
}
func (r measuredReadRepository) ListPublicFacts(ctx context.Context, ids []int64) ([]articleread.CurrentFact, error) {
	v, e := r.ArticleRepository.ListPublicFacts(ctx, ids)
	r.record(0, len(v), v)
	return v, e
}
func (r measuredReadRepository) ListPublicFragments(ctx context.Context, ids []articlecache.CardIdentity) ([]articlecache.CardFragment, error) {
	v, e := r.ArticleRepository.ListPublicFragments(ctx, ids)
	r.record(1, len(v), v)
	return v, e
}
func (r measuredReadRepository) ListLatestCandidates(ctx context.Context, q articlecache.LatestQuery) (articlecache.LatestPage, error) {
	v, e := r.ArticleRepository.ListLatestCandidates(ctx, q)
	r.record(2, len(v.Candidates), v.Candidates)
	return v, e
}
func (r measuredReadRepository) ListRecommendationCandidates(ctx context.Context, c *articleDomain.Cursor, n int, skip []int64, user string, now, start time.Time) ([]articlecache.Candidate, error) {
	v, e := r.ArticleRepository.ListRecommendationCandidates(ctx, c, n, skip, user, now, start)
	r.record(2, len(v), v)
	return v, e
}

type measuredSnapshot struct {
	*postgres.TxManager
	counts *measuredReads
}

func (s measuredSnapshot) WithinReadSnapshot(ctx context.Context, fn func(context.Context, bool) error) error {
	start := time.Now()
	e := s.TxManager.WithinReadSnapshot(ctx, fn)
	s.counts.snapshots.Add(1)
	s.counts.viewNS.Add(int64(time.Since(start)))
	return e
}

// 固定业务时钟用于复现语料排序；Redis 的绝对寿命仍从该真实请求开始计时。
type loadStartKey struct{}
type wallPlanCache struct{ articlecache.Cache }

func (c wallPlanCache) NewRequest(ctx context.Context) context.Context {
	if ctx.Value(loadStartKey{}) == nil {
		ctx = context.WithValue(ctx, loadStartKey{}, time.Now().UTC())
	}
	return c.Cache.NewRequest(ctx)
}
func (c wallPlanCache) PutPlan(ctx context.Context, id articlecache.PlanIdentity, p articlecache.RecommendationPlan, _ time.Time) error {
	return c.Cache.PutPlan(ctx, id, p, ctx.Value(loadStartKey{}).(time.Time))
}

func TestCacheReadComparisonAndJointRecovery(t *testing.T) {
	env, stack, feedback, now, users, fixture := prepareCacheReadLoad(t)
	proxyHarness := redistest.New(t)
	proxy := redistest.NewProxy(t, proxyHarness.Address)
	// 重新装配经自有代理的适配器，仍只清理这个适配器登记的键。
	registry := prometheus.NewRegistry()
	metrics := observability.NewCacheMetrics(registry, nil)
	cache, h := newOwnedReadCache(t, proxy.Address(), metrics)
	paths := cacheReadPaths
	ctx := context.Background()
	var expected map[string]string
	var report strings.Builder
	fmt.Fprintf(&report, "固定语料 SHA256: `%x`；1000 篇、两名用户、limit=20、并发5、每 worker10轮、六请求。\n\n", sha256.Sum256([]byte(strings.Join(fixture, "\n"))))
	for _, mode := range []string{"disabled", "cold", "hot", "corrupt", "failure", "recovery"} {
		t.Run(mode, func(t *testing.T) {
			counts := &readCounts{}
			reads := &measuredReads{}
			original := countedReader{postgres.NewArticleRepository(env.pool), counts}
			var reader recommendation.Reader = original
			var public searchApp.PublicArticleReader = original
			latest := articleApp.NewService(original, nil, nil)
			if mode != "disabled" {
				shared := articleread.NewService(measuredReadRepository{original.ArticleRepository, reads}, measuredSnapshot{postgres.NewTxManager(env.pool), reads}, cache, metrics, 100, nil)
				reader, public = shared, shared
				latest.WithLatestReader(shared)
			}
			codec, _ := searchApp.NewCursorCodec([]byte(strings.Repeat("s", 32)))
			cfg := searchApp.Config{PITKeepAlive: time.Minute, CandidateBatchSize: 100, MaxCandidatesPerRequest: 500, Timeout: 5 * time.Second}
			bm := searchApp.NewService(stack.client, public, codec, nil, cfg)
			cfg.Hybrid = searchApp.HybridConfig{Enabled: true, BM25Candidates: 100, KNNCandidates: 100, EmbeddingTimeout: time.Second, KNNTimeout: time.Second, Profile: "e-v1", Dimensions: 3}
			hybrid := searchApp.NewService(stack.client, public, codec, nil, cfg).WithHybrid(countedVector{counts}, reader)
			rc, _ := recommendation.NewCursorCodec([]byte(strings.Repeat("r", 32)))
			rec := recommendation.NewService(feedbackApp.NewProfileService(feedback, profileClock{now}), countedIndex{stack.client, stack.client, counts}, reader, rc, countedVector{counts}, recommendation.Config{FirstQueryTimeout: 5 * time.Second, BM25Candidates: 100, KNNCandidates: 100, CursorTTL: 2 * time.Minute, SemanticEnabled: true, EmbeddingProfile: "e-v1", Dimensions: 3, EmbeddingTimeout: time.Second, KNNTimeout: time.Second}, func() time.Time { return now })
			if mode != "disabled" {
				rec.WithPlanCache(wallPlanCache{cache}, func(ctx context.Context) { metrics.Fallback(ctx, articlecache.Recommend, 1) })
			}
			if mode == "cold" || mode == "recovery" {
				if e := h.Clear(); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "hot" || mode == "corrupt" {
				runCacheReadLoad(t, users, latest, bm, hybrid, rec, closeLoadBM(t, codec, stack))
			}
			if mode == "corrupt" {
				for _, key := range h.Keys() {
					if e := h.Client.Set(ctx, key, "损坏值", time.Minute).Err(); e != nil {
						t.Fatal(e)
					}
				}
			}
			proxy.Disconnect(mode == "failure")
			counts.calls.Store(0)
			counts.rows.Store(0)
			counts.bytes.Store(0)
			counts.recall.Store(0)
			counts.embedding.Store(0)
			for i := range reads.calls {
				reads.calls[i].Store(0)
				reads.rows[i].Store(0)
				reads.bytes[i].Store(0)
			}
			reads.snapshots.Store(0)
			reads.viewNS.Store(0)
			metrics.Entries.Reset()
			metrics.Failures.Reset()
			metrics.FallbackBatches.Reset()
			metrics.Duration.Reset()
			durations, outputs := runCacheReadLoad(t, users, latest, bm, hybrid, rec, closeLoadBM(t, codec, stack))
			if expected == nil {
				expected = outputs
			} else if !reflect.DeepEqual(expected, outputs) {
				t.Fatal("缓存状态改变固定请求的输出ID")
			}
			fmt.Fprintf(&report, "### %s\n\n| 路径 | 请求数 | P50 ms | P95 ms |\n| --- | ---: | ---: | ---: |\n", mode)
			for _, path := range paths {
				v := durations[path]
				sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
				if len(v) != 50 {
					t.Fatal("负载不完整", path, len(v))
				}
				fmt.Fprintf(&report, "| %s | 50 | %.3f | %.3f |\n", path, float64(v[24])/float64(time.Millisecond), float64(v[47])/float64(time.Millisecond))
			}
			fmt.Fprintf(&report, "\n原卡片端口：%d 次/%d 行/%d JSON字节；事实：%d 次/%d 行/%d JSON字节；缺失片段：%d 次/%d 行/%d JSON字节；候选ID：%d 次/%d 行/%d JSON字节。快照 %d 次，端口占用总耗时 %.3f ms；推荐召回 %d 次；Embedding %d 次。\n\n", counts.calls.Load(), counts.rows.Load(), counts.bytes.Load(), reads.calls[0].Load(), reads.rows[0].Load(), reads.bytes[0].Load(), reads.calls[1].Load(), reads.rows[1].Load(), reads.bytes[1].Load(), reads.calls[2].Load(), reads.rows[2].Load(), reads.bytes[2].Load(), reads.snapshots.Load(), float64(reads.viewNS.Load())/float64(time.Millisecond), counts.recall.Load(), counts.embedding.Load())
			families, e := registry.Gather()
			if e != nil {
				t.Fatal(e)
			}
			hits := map[string]float64{}
			for _, family := range families {
				if !strings.HasSuffix(family.GetName(), "_total") {
					continue
				}
				for _, metric := range family.Metric {
					labels := []string{}
					object, result := "", ""
					for _, l := range metric.Label {
						labels = append(labels, l.GetName()+"="+l.GetValue())
						if l.GetName() == "object" {
							object = l.GetValue()
						}
						if l.GetName() == "result" {
							result = l.GetValue()
						}
					}
					fmt.Fprintf(&report, "- `%s{%s}` = %.0f\n", family.GetName(), strings.Join(labels, ","), metric.GetCounter().GetValue())
					if result == "hit" {
						hits[object] += metric.GetCounter().GetValue()
					}
				}
			}
			if mode == "hot" || mode == "recovery" {
				for _, object := range []string{"card", "latest", "recommend"} {
					if hits[object] == 0 {
						t.Fatal("恢复没有三对象命中", object, hits)
					}
				}
			}
			fmt.Fprintln(&report)
		})
	}
	proxy.Disconnect(false)
	t.Log(report.String())
	if path := os.Getenv("VELIS_TEST_CACHE_COMPARISON_REPORT"); path != "" && !t.Failed() {
		if e := os.WriteFile(path, []byte(report.String()), 0600); e != nil {
			t.Fatal(e)
		}
	}
}
