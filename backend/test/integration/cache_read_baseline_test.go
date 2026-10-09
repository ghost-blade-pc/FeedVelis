package integration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	feedbackApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlefeedback"
	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

// readCounts 统计应用端口调用、返回卡片及其 JSON 字节；字节不是数据库网络流量。
type readCounts struct{ calls, rows, bytes, recall, embedding atomic.Int64 }
type countedReader struct {
	*postgres.ArticleRepository
	counts *readCounts
}

func (r countedReader) record(items []articleDomain.ListItem) {
	raw, _ := json.Marshal(items)
	r.counts.calls.Add(1)
	r.counts.rows.Add(int64(len(items)))
	r.counts.bytes.Add(int64(len(raw)))
}
func (r countedReader) ListPublished(ctx context.Context, c *articleDomain.Cursor, n int) ([]articleDomain.ListItem, error) {
	v, e := r.ArticleRepository.ListPublished(ctx, c, n)
	r.record(v)
	return v, e
}
func (r countedReader) ListPublishedByIDs(ctx context.Context, ids []int64) ([]articleDomain.ListItem, error) {
	v, e := r.ArticleRepository.ListPublishedByIDs(ctx, ids)
	r.record(v)
	return v, e
}
func (r countedReader) ListPublishedWithIdentity(ctx context.Context, ids []int64) ([]searchApp.CurrentArticle, error) {
	v, e := r.ArticleRepository.ListPublishedWithIdentity(ctx, ids)
	items := make([]articleDomain.ListItem, len(v))
	for i := range v {
		items[i] = v[i].Item
	}
	r.record(items)
	return v, e
}
func (r countedReader) ListRecommendationLatest(ctx context.Context, c *articleDomain.Cursor, n int, skip []int64, user string, now, start time.Time) ([]articleDomain.ListItem, error) {
	v, e := r.ArticleRepository.ListRecommendationLatest(ctx, c, n, skip, user, now, start)
	r.record(v)
	return v, e
}

type countedIndex struct {
	recommendation.CandidateIndex
	hybrid recommendation.HybridCandidateIndex
	counts *readCounts
}

func (i countedIndex) Recall(ctx context.Context, t recommendation.Terms, n int, ttl time.Duration) ([]searchApp.Candidate, error) {
	i.counts.recall.Add(1)
	return i.CandidateIndex.Recall(ctx, t, n, ttl)
}
func (i countedIndex) RecallHybrid(ctx context.Context, t recommendation.Terms, b, k int, ttl time.Duration, v []float64, p string, timeout time.Duration) (recommendation.HybridRecall, error) {
	i.counts.recall.Add(1)
	return i.hybrid.RecallHybrid(ctx, t, b, k, ttl, v, p, timeout)
}

type countedVector struct{ counts *readCounts }

func (v countedVector) EmbedQuery(context.Context, string) ([]float64, error) {
	v.counts.embedding.Add(1)
	return []float64{0.25, 0.5, 0.75}, nil
}

// TestCacheReadBaseline 在任何业务读取路径变动前建立固定负载，后续冷/热/故障比较复用此入口。
func TestCacheReadBaseline(t *testing.T) {
	env, stack, feedback, now, users, fixture := prepareCacheReadLoad(t)
	counts := &readCounts{}
	reader := countedReader{postgres.NewArticleRepository(env.pool), counts}
	latest := articleApp.NewService(reader, nil, nil)
	codec, _ := searchApp.NewCursorCodec([]byte(strings.Repeat("s", 32)))
	bm := searchApp.NewService(stack.client, reader, codec, nil, searchApp.Config{PITKeepAlive: time.Minute, CandidateBatchSize: 100, MaxCandidatesPerRequest: 500, Timeout: 5 * time.Second})
	hybrid := searchApp.NewService(stack.client, reader, codec, nil, searchApp.Config{PITKeepAlive: time.Minute, CandidateBatchSize: 100, MaxCandidatesPerRequest: 500, Timeout: 5 * time.Second, Hybrid: searchApp.HybridConfig{Enabled: true, BM25Candidates: 100, KNNCandidates: 100, EmbeddingTimeout: time.Second, KNNTimeout: time.Second, Profile: "e-v1", Dimensions: 3}}).WithHybrid(countedVector{counts}, reader)
	recCodec, _ := recommendation.NewCursorCodec([]byte(strings.Repeat("r", 32)))
	rec := recommendation.NewService(feedbackApp.NewProfileService(feedback, profileClock{now}), countedIndex{stack.client, stack.client, counts}, reader, recCodec, countedVector{counts}, recommendation.Config{FirstQueryTimeout: 5 * time.Second, BM25Candidates: 100, KNNCandidates: 100, CursorTTL: 2 * time.Minute, SemanticEnabled: true, EmbeddingProfile: "e-v1", Dimensions: 3, EmbeddingTimeout: time.Second, KNNTimeout: time.Second}, func() time.Time { return now })
	durations, outputs := runCacheReadLoad(t, users, latest, bm, hybrid, rec, closeLoadBM(t, codec, stack))
	_ = outputs
	paths := cacheReadPaths
	var report strings.Builder
	fmt.Fprintf(&report, "固定语料 SHA256: `%x`；1000 篇、两名用户、limit=20、并发 5、每 worker 10 轮、每轮固定 6 请求；固定时钟 `%s`。\n\n", sha256.Sum256([]byte(strings.Join(fixture, "\n"))), now.Format(time.RFC3339))
	fmt.Fprintln(&report, "| 路径 | 请求数 | P50 ms | P95 ms |\n| --- | ---: | ---: | ---: |")
	for _, path := range paths {
		v := durations[path]
		sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
		if len(v) != 50 {
			t.Fatalf("负载不完整 %s: %d", path, len(v))
		}
		fmt.Fprintf(&report, "| %s | %d | %.3f | %.3f |\n", path, len(v), float64(v[24])/float64(time.Millisecond), float64(v[47])/float64(time.Millisecond))
	}
	fmt.Fprintf(&report, "\n卡片数据库端口读取 %d 次、返回 %d 卡片、JSON 载荷 %d 字节；推荐召回 %d 次；查询 Embedding %d 次（确定性桩，无收费模型）。画像/排除 SQL 不计入卡片端口计数；PIT、查询及 KNN 次数由固定序列确定：搜索 BM25 100、搜索 KNN 50，推荐 BM25/KNN 各 50。\n", counts.calls.Load(), counts.rows.Load(), counts.bytes.Load(), counts.recall.Load(), counts.embedding.Load())
	t.Log(report.String())
	if path := os.Getenv("VELIS_TEST_CACHE_REPORT"); path != "" && !t.Failed() {
		if err := os.WriteFile(path, []byte(report.String()), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *projectionE2E) envArticleRepository() *postgres.ArticleRepository {
	return postgres.NewArticleRepository(e.env.pool)
}

var cacheReadPaths = []string{"latest_first", "latest_next", "bm25_assembly", "hybrid_assembly", "recommend_first", "recommend_next"}

func prepareCacheReadLoad(t *testing.T) (*testEnv, *projectionE2E, *postgres.ArticleFeedbackRepository, time.Time, []string, []string) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	const author = "c1000000-0000-0000-0000-000000000001"
	users := []string{"c1000000-0000-0000-0000-000000000002", "c1000000-0000-0000-0000-000000000003"}
	seedIntegrationUser(t, env, author, "cache_author", "user", now)
	for i, u := range users {
		seedIntegrationUser(t, env, u, fmt.Sprintf("cache_user_%d", i), "user", now)
	}
	stack := newProjectionE2E(t, env, 3)
	feedback := postgres.NewArticleFeedbackRepository(env.pool)
	var fixture []string
	for i := 0; i < 1000; i++ {
		label := []string{"Go", "Rust"}[i%2]
		at := now.Add(-time.Duration(i) * time.Minute)
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("cache-fixture-v1:%d", i))))
		markdown, html := "固定语料 "+label, "<p>固定语料 "+label+"</p>"
		stored, err := stack.envArticleRepository().CreateUserArticle(ctx, author, articleDomain.RevisionData{Title: "投影文章 " + label, Markdown: &markdown, SanitizedHTML: &html, PlainText: markdown, Excerpt: markdown, Language: "zh-CN", ContentHash: hash, SanitizerVersion: 1}, true, at)
		if err != nil {
			t.Fatal(err)
		}
		seedRecommendationLabels(t, env, stored.ID, label, at)
		if i < 2 {
			if err := feedback.SetFavorite(ctx, users[i], stored.ID, true, now); err != nil {
				t.Fatal(err)
			}
		}
		fixture = append(fixture, fmt.Sprintf("%d:%s:%s", stored.ID, hash, at.Format(time.RFC3339)))
	}
	// v2 真索引 + 固定三维向量；语料无向量，真实 KNN 为空，语义路径确定性执行。
	rebuild := projectionApp.NewRebuildService(stack.repository, stack.repository, stack.client, stack.client, stack.repository, projectionApp.RebuildPolicy{IndexPrefix: stack.prefix, SchemaVersion: 2, SchemaIdentity: "mapping-v2|analyzer-cjk|dims-3|encoding-v2", EmbeddingDimensions: 3, SnapshotBatch: 100, RollbackWindow: time.Hour, Lease: 2 * time.Second, SampleSize: 10, PollInterval: 20 * time.Millisecond}, nil)
	state, err := rebuild.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stack.client.Delete(context.Background(), state.CandidateIndex) })
	if _, err = rebuild.Cutover(ctx, state.ID); err != nil {
		t.Fatal(err)
	}
	if err = stack.client.Refresh(ctx, state.CandidateIndex); err != nil {
		t.Fatal(err)
	}
	return env, stack, feedback, now, users, fixture
}
func runCacheReadLoad(t *testing.T, users []string, latest *articleApp.Service, bm, hybrid *searchApp.Service, rec *recommendation.Service, closeBM ...func(string)) (map[string][]time.Duration, map[string]string) {
	ctx := context.Background()
	paths := cacheReadPaths
	var mu sync.Mutex
	durations := map[string][]time.Duration{}
	outputs := map[string]string{}
	var wg sync.WaitGroup
	for worker := 0; worker < 5; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for round := 0; round < 10; round++ {
				user := users[round%2]
				var latestToken, recToken string
				for _, path := range paths {
					start := time.Now()
					var ids []int64
					var e error
					var pitToken string
					if strings.HasPrefix(path, "latest") {
						var p articleApp.Page
						p, e = latest.List(ctx, latestToken, 20)
						for _, item := range p.Items {
							ids = append(ids, item.ID)
						}
						if p.NextCursor != nil {
							latestToken = *p.NextCursor
						}
					}
					if path == "bm25_assembly" || path == "hybrid_assembly" {
						s := bm
						if path == "hybrid_assembly" {
							s = hybrid
						}
						var p searchApp.Page
						p, e = s.Search(ctx, searchApp.Request{Q: "投影文章", Limit: 20})
						if path == "bm25_assembly" && p.NextCursor != nil {
							pitToken = *p.NextCursor
						}
						for _, item := range p.Items {
							ids = append(ids, item.ID)
						}
					}
					if strings.HasPrefix(path, "recommend") {
						var p recommendation.Page
						p, e = rec.Get(ctx, user, 20, recToken)
						for _, item := range p.Items {
							ids = append(ids, item.Article.ID)
						}
						if p.NextCursor != nil {
							recToken = *p.NextCursor
						}
					}
					d := time.Since(start)
					if pitToken != "" && len(closeBM) > 0 {
						closeBM[0](pitToken)
					}
					if e != nil {
						t.Errorf("%s: %v", path, e)
						return
					}
					if len(ids) != 20 {
						t.Errorf("%s 返回数量 %d", path, len(ids))
						return
					}
					key := fmt.Sprintf("%s:%d", path, round%2)
					value := fmt.Sprint(ids)
					mu.Lock()
					if previous, ok := outputs[key]; ok && previous != value {
						t.Errorf("固定序列输出变化: %s", key)
					}
					outputs[key] = value
					durations[path] = append(durations[path], d)
					mu.Unlock()
				}
			}
		}(worker)
	}
	wg.Wait()
	return durations, outputs
}

// 固定负载只比较搜索首查；测量后关闭自身游标 PIT，避免多状态循环占满服务端上下文。
func closeLoadBM(t *testing.T, codec *searchApp.CursorCodec, stack *projectionE2E) func(string) {
	return func(token string) {
		pit, _, err := codec.Decode(searchApp.Query{Q: "投影文章"}, token, time.Now())
		if err != nil {
			t.Error("解析自有PIT", err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err = stack.client.ClosePIT(ctx, pit); err != nil {
			t.Error("关闭自有PIT", err)
		}
	}
}
