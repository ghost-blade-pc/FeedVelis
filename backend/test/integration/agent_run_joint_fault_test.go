package integration

import (
	"encoding/json"
	conversationApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentconversation"
	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agenttools"
	feedbackApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlefeedback"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleread"
	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/health"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	eino "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/ai/eino"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	searchAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/search/opensearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/test/testkit/redistest"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAgentRunGenerationFaultIsolationAndDependencyRecovery(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "generation_disabled"
		if enabled {
			name = "local_model_fault"
		}
		t.Run(name, func(t *testing.T) {
			env := newTestEnv(t)
			seedAgentSessions(t, env)
			ctx := t.Context()
			now := time.Now().Add(-time.Hour)
			ids := []int64{}
			for _, hash := range []string{"a", "b", "c"} {
				ids = append(ids, createPublishedArticle(t, env, agentTestUser, hash, now))
			}
			seedRecommendationLabels(t, env, ids[0], "Go", now)
			stack := newProjectionE2E(t, env, 3)
			stack.drain(t, ctx)
			if err := stack.client.Refresh(ctx, stack.index); err != nil {
				t.Fatal(err)
			}
			redis := redistest.New(t)
			redisProxy := redistest.NewProxy(t, redis.Address)
			cache, harness := newOwnedReadCache(t, redisProxy.Address())
			endpoint, err := url.Parse(os.Getenv("VELIS_TEST_OPENSEARCH_URL"))
			if err != nil {
				t.Fatal(err)
			}
			searchProxy := redistest.NewProxy(t, endpoint.Host)
			index, err := searchAdapter.New(searchAdapter.Config{Endpoints: []string{endpoint.Scheme + "://" + searchProxy.Address()}, IndexPrefix: stack.prefix, SchemaVersion: 1, SchemaIdentity: "mapping-v1|analyzer-cjk|dims-3|encoding-v1", EmbeddingDimensions: 3, ConnectTimeout: time.Second, RequestTimeout: time.Second, BulkMaxItems: 100, BulkMaxBytes: 1 << 20, BulkMaxDocumentChars: 100000})
			if err != nil {
				t.Fatal(err)
			}
			defer index.Close()
			repo := postgres.NewArticleRepository(env.pool)
			tx := postgres.NewTxManager(env.pool)
			shared := articleread.NewService(repo, tx, cache, nil, 100, nil)
			sc, _ := searchApp.NewCursorCodec([]byte(strings.Repeat("s", 32)))
			search := searchApp.NewService(index, shared, sc, nil, searchApp.Config{Timeout: time.Second, PITKeepAlive: time.Minute, CandidateBatchSize: 100, MaxCandidatesPerRequest: 500})
			feedback := postgres.NewArticleFeedbackRepository(env.pool)
			if err := feedback.SetFavorite(ctx, agentTestUser, ids[0], true, now); err != nil {
				t.Fatal(err)
			}
			profile := feedbackApp.NewProfileService(feedback, profileClock{time.Now()})
			rc, _ := recommendation.NewCursorCodec([]byte(strings.Repeat("r", 32)))
			rec := recommendation.NewService(profile, index, shared, rc, nil, recommendation.Config{FirstQueryTimeout: time.Second, BM25Candidates: 100, KNNCandidates: 100, CursorTTL: 2 * time.Minute}, time.Now).WithPlanCache(cache)
			var calls atomic.Int32
			var fault atomic.Bool
			fault.Store(true)
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if fault.Load() {
					http.Error(w, "private-provider-error", 503)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": `{"constraints":{},"clarification":"unsupported_capability","require_body":false}`}, "finish_reason": "stop"}}})
			}))
			defer model.Close()
			cfg := config.Default()
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			cfg.Agent.Generation.BaseURL = model.URL + "/v1"
			cfg.Agent.Generation.Model = "local-isolation-stub"
			cfg.Agent.Generation.APIKey = config.SecretBytes("local-test-only")
			runRepo := postgres.NewAgentRunRepository(env.pool)
			runner := eino.NewReadingRunner(cfg.Agent, agenttools.NewService(search, rec, repo, agenttools.Config{}), agent.NewGuard(runRepo, runRepo, tx), postgres.NewAgentEvidenceFacts(env.pool))
			_, s := startExecutor(t, env, runner, enabled)
			c, m := agentQuestion(t, env, 1)
			core := newAgentService(env, conversationApp.DefaultLimits())
			baselinePIT := agentOpenPITs(t)
			for _, phase := range []string{"warm", "dependencies_down", "recovered"} {
				down := phase == "dependencies_down"
				redisProxy.Disconnect(down)
				searchProxy.Disconnect(down)
				if phase == "warm" {
					if err := harness.Clear(); err != nil {
						t.Fatal(err)
					}
				}
				if err := health.NewService(env.pool).Ready(ctx); err != nil {
					t.Fatal("模型或可选依赖破坏 readiness", err)
				}
				if _, err := core.Detail(ctx, agentTestUser, c.ID); err != nil {
					t.Fatal(err)
				}
				page, err := shared.ListPublishedWithIdentity(ctx, ids)
				if err != nil || len(page) != 3 {
					t.Fatal("读取缓存降级失败", page, err)
				}
				searchResult, searchErr := search.SearchOnce(ctx, searchApp.Request{Q: "投影文章", Limit: 10})
				if down {
					if searchApp.CodeOf(searchErr) != searchApp.CodeSearchUnavailable {
						t.Fatal(searchErr)
					}
				} else if searchErr != nil || len(searchResult.Items) == 0 {
					t.Fatal(searchResult, searchErr)
				}
				result, err := rec.GetOnce(ctx, agentTestUser, 10)
				if err != nil || len(result.Items) == 0 {
					t.Fatal(result, err)
				}
				if down && (!result.Degraded || result.Mode != "latest_fallback") {
					t.Fatal("依赖故障未保留推荐回退", result)
				}
				if phase == "dependencies_down" {
					accepted, err := s.Accept(ctx, agentCaller, c.ID, agentKey(1), agent.Command{MessageID: m.ID})
					if enabled {
						if err != nil {
							t.Fatal(err)
						}
						record := awaitRun(t, runRepo, agent.Scope{UserID: agentTestUser, ConversationID: c.ID, RunID: accepted.Snapshot.ID}, run.Failed, 3*time.Second)
						if record.Run.ErrorCode != run.CodeModelUnavailable || record.Run.AssistantReserved {
							t.Fatal(record)
						}
						fault.Store(false)
						retry := accepted.Snapshot.ID
						next, err := s.Accept(ctx, agentCaller, c.ID, agentKey(2), agent.Command{MessageID: m.ID, RetryOf: &retry})
						if err != nil {
							t.Fatal(err)
						}
						awaitRun(t, runRepo, agent.Scope{UserID: agentTestUser, ConversationID: c.ID, RunID: next.Snapshot.ID}, run.Succeeded, 3*time.Second)
					} else if agent.SafeCode(err) != run.CodeGenerationUnavailable || calls.Load() != 0 {
						t.Fatal(err, calls.Load())
					}
					if _, err := core.Append(ctx, agentTestUser, c.ID, agentKey(3), "核心消息仍可保存"); err != nil {
						t.Fatal(err)
					}
				}
				if current := agentOpenPITs(t); current != baselinePIT {
					t.Fatal("故障恢复泄漏 PIT", baselinePIT, current)
				}
				t.Logf("联合故障 mode=%s phase=%s readiness=ready articles=%d recommend_mode=%s degraded=%v PIT=%d model_calls=%d", name, phase, len(page), result.Mode, result.Degraded, baselinePIT, calls.Load())
			}
		})
	}
}
