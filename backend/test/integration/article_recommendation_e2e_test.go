package integration

import (
	"context"
	"fmt"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlecache"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleread"
	"github.com/ghost-blade-pc/Velis_Feed/backend/test/testkit/redistest"
	"os"
	"strings"
	"testing"
	"time"

	feedbackApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlefeedback"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	searchAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/search/opensearch"
	"github.com/google/uuid"
)

func TestArticleRecommendationPostgresOpenSearch(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Hour)
	const author = "a1000000-0000-0000-0000-000000000001"
	const userA = "a1000000-0000-0000-0000-000000000002"
	const userB = "a1000000-0000-0000-0000-000000000003"
	seedIntegrationUser(t, env, author, "recommend_author", "user", now)
	seedIntegrationUser(t, env, userA, "recommend_a", "user", now)
	seedIntegrationUser(t, env, userB, "recommend_b", "user", now)
	stack := newProjectionE2E(t, env, 3)
	goArticle := createPublishedArticle(t, env, author, "a", now)
	rustArticle := createPublishedArticle(t, env, author, "b", now)
	otherArticle := createPublishedArticle(t, env, author, "c", now)
	seedRecommendationLabels(t, env, goArticle, "Go", now)
	seedRecommendationLabels(t, env, rustArticle, "Rust", now)
	feedbackRepository := postgres.NewArticleFeedbackRepository(env.pool)
	if err := feedbackRepository.SetFavorite(ctx, userA, goArticle, true, now); err != nil {
		t.Fatal(err)
	}
	if err := feedbackRepository.SetFavorite(ctx, userB, rustArticle, true, now); err != nil {
		t.Fatal(err)
	}
	codec, _ := recommendation.NewCursorCodec([]byte(strings.Repeat("r", 32)))
	var reader *articleread.Service
	var planCache articlecache.Cache
	var planHarness *redistest.Harness
	if os.Getenv("VELIS_TEST_REDIS_ADDRESS") != "" {
		planCache, planHarness = newOwnedReadCache(t, "")
		reader = articleread.NewService(postgres.NewArticleRepository(env.pool), postgres.NewTxManager(env.pool), planCache, nil, 100, nil)
		t.Log("推荐首查计划已接入真实 Redis，OpenSearch 回归确实执行")
	} else {
		reader = sharedIntegrationReader(t, env)
	}
	clearPlans := func() {
		if planHarness != nil {
			if err := planHarness.Clear(); err != nil {
				t.Fatal(err)
			}
		}
	}

	profile := feedbackApp.NewProfileService(feedbackRepository, profileClock{time.Now().UTC()})
	newService := func(index recommendation.CandidateIndex) *recommendation.Service {
		return recommendation.NewService(profile, index, reader, codec, nil, recommendation.Config{FirstQueryTimeout: 5 * time.Second, BM25Candidates: 100, KNNCandidates: 100, CursorTTL: 2 * time.Minute}, time.Now).WithPlanCache(planCache)
	}
	service := newService(stack.client)
	// 投影尚未追赶时 BM25 为空，推荐仍由 PostgreSQL latest 补页。
	emptyBM, err := service.Get(ctx, userA, 2, "")
	if err != nil || emptyBM.Mode != "personalized" || !emptyBM.Degraded || len(emptyBM.Items) != 2 {
		t.Fatalf("空 BM25 未补位: %+v %v", emptyBM, err)
	}
	stack.drain(t, ctx)
	if err := stack.client.Refresh(ctx, stack.index); err != nil {
		t.Fatal(err)
	}
	// 成功空召回计划有 30 秒窗口；投影追赶后的独立回归显式清理本次缓存。
	clearPlans()
	firstA, err := service.Get(ctx, userA, 1, "")
	if err != nil || len(firstA.Items) != 1 || firstA.Items[0].Article.ID != goArticle || firstA.NextCursor == nil {
		t.Fatalf("用户 A 推荐未命中偏好: %+v %v", firstA, err)
	}
	firstB, err := service.Get(ctx, userB, 1, "")
	if err != nil || len(firstB.Items) != 1 || firstB.Items[0].Article.ID != rustArticle {
		t.Fatalf("用户 B 推荐未隔离偏好: %+v %v", firstB, err)
	}
	if _, err := service.Get(ctx, userB, 1, *firstA.NextCursor); err != recommendation.ErrInvalidCursor {
		t.Fatalf("跨用户游标未拒绝: %v", err)
	}
	// 切换真实 v2 读别名：无文章向量时 KNN 返回空集，BM25 仍应命中偏好词项。
	rebuild := projectionApp.NewRebuildService(stack.repository, stack.repository, stack.client, stack.client, stack.repository,
		projectionApp.RebuildPolicy{IndexPrefix: stack.prefix, SchemaVersion: 2,
			SchemaIdentity: "mapping-v2|analyzer-cjk|dims-3|encoding-v2", EmbeddingDimensions: 3,
			SnapshotBatch: 10, RollbackWindow: time.Hour, Lease: 2 * time.Second, SampleSize: 10, PollInterval: 20 * time.Millisecond}, nil)
	state, err := rebuild.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stack.client.Delete(context.Background(), state.CandidateIndex) })
	if _, err := rebuild.Cutover(ctx, state.ID); err != nil {
		t.Fatal(err)
	}
	semantic := recommendation.NewService(profile, stack.client, reader, codec, recommendationVector{}, recommendation.Config{
		FirstQueryTimeout: 5 * time.Second, BM25Candidates: 100, KNNCandidates: 100, CursorTTL: 2 * time.Minute,
		SemanticEnabled: true, EmbeddingProfile: "e-v1", Dimensions: 3, EmbeddingTimeout: time.Second, KNNTimeout: time.Second,
	}, time.Now).WithPlanCache(planCache)
	noKNN, err := semantic.Get(ctx, userA, 1, "")
	if err != nil || len(noKNN.Items) != 1 || noKNN.Items[0].Article.ID != goArticle || noKNN.Mode != "personalized" {
		t.Fatalf("真实 v2 KNN 空集未保留 BM25: %+v %v", noKNN, err)
	}
	if err := feedbackRepository.SetNotInterested(ctx, userA, rustArticle, true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE velis.articles SET status='offline',offline_reason='author',offline_at=now() WHERE id=$1`, otherArticle); err != nil {
		t.Fatal(err)
	}
	secondA, err := service.Get(ctx, userA, 2, *firstA.NextCursor)
	if err != nil || len(secondA.Items) != 0 || secondA.HasMore {
		t.Fatalf("续页泄露下架或新负反馈: %+v %v", secondA, err)
	}
	badClient, err := searchAdapter.New(searchAdapter.Config{Endpoints: []string{"http://127.0.0.1:1"}, IndexPrefix: "unavailable", ConnectTimeout: 100 * time.Millisecond, RequestTimeout: 100 * time.Millisecond, QueryTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer badClient.Close()
	clearPlans()
	fallback, err := newService(badClient).Get(ctx, userA, 2, "")
	if err != nil || fallback.Mode != "latest_fallback" || !fallback.Degraded || len(fallback.Items) == 0 || fallback.Items[0].Reason != "latest_fallback" {
		t.Fatalf("OpenSearch 连接故障未按 latest 回退: %+v %v", fallback, err)
	}
	recovered, err := newService(stack.client).Get(ctx, userA, 1, "")
	if err != nil || recovered.Mode != "personalized" {
		t.Fatal("搜索恢复未重新召回", recovered, err)
	}
}

type recommendationVector struct{}

func (recommendationVector) EmbedQuery(context.Context, string) ([]float64, error) {
	return []float64{0.25, 0.5, 0.75}, nil
}

func seedRecommendationLabels(t *testing.T, env *testEnv, articleID int64, keyword string, now time.Time) {
	t.Helper()
	var revisionID int64
	if err := env.pool.QueryRow(context.Background(), `SELECT current_revision_id FROM velis.articles WHERE id=$1`, articleID).Scan(&revisionID); err != nil {
		t.Fatal(err)
	}
	resultID := uuid.NewString()
	_, err := env.pool.Exec(context.Background(), `INSERT INTO velis.ai_generation_results
(id,article_id,revision_id,provider,model,profile_version,workflow_version,prompt_version,generation_input_hash,input_truncated,summary,keywords,topics,generated_at)
VALUES($1,$2,$3,'stub','chat','g-v1','w-v1','p-v1',$4,false,'摘要',$5,ARRAY['技术'],$6)`, resultID, articleID, revisionID, fmt.Sprintf("%064x", articleID), []string{keyword}, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.pool.Exec(context.Background(), `INSERT INTO velis.ai_current_selections(article_id,revision_id,generation_result_id,generation_profile_version,updated_at)
VALUES($1,$2,$3,'g-v1',$4)`, articleID, revisionID, resultID, now)
	if err != nil {
		t.Fatal(err)
	}
}
