package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agenttools"
	feedbackApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlefeedback"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articleread"
	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	"github.com/ghost-blade-pc/Velis_Feed/backend/test/testkit/redistest"
	"github.com/jackc/pgx/v5/pgxpool"
)

func agentPtr[T any](v T) *T { return &v }

func TestAgentToolsPublicTextCurrentRevisionAndVisibility(t *testing.T) {
	env := newTestEnv(t)
	seedAgentUsers(t, env)
	repo := postgres.NewArticleRepository(env.pool)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Hour)
	id := createPublishedArticle(t, env, agentTestUser, "a", now)
	facts, err := repo.ListPublishedWithIdentity(ctx, []int64{id})
	if err != nil || len(facts) != 1 {
		t.Fatal(err)
	}
	oldRevision := facts[0].Identity.RevisionID
	insertGenerationResult(t, env, agentKey(500), "p1", id, oldRevision)
	setCurrentSelection(t, env, id, oldRevision, agentPtr(agentKey(500)), nil)
	s := agenttools.NewService(nil, nil, repo, agenttools.Config{})
	caller := agenttools.Caller{UserID: agentTestUser}
	before, err := s.Get(ctx, caller, agenttools.GetInput{ArticleID: &id})
	if err != nil || before.RevisionID != oldRevision || before.SummarySource != "model" {
		t.Fatalf("当前增强: %+v %v", before, err)
	}
	_, changed, err := repo.UpdateUserRevision(ctx, id, agentTestUser, 1, articleDomain.RevisionData{Title: "新修订", PlainText: "😀中文新正文", Excerpt: "当前摘录", Language: "zh-CN", ContentHash: strings.Repeat("b", 64), SanitizerVersion: 1}, now.Add(time.Minute))
	if err != nil || !changed {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, caller, agenttools.GetInput{ArticleID: &id, MaxChars: agentPtr(3)})
	if err != nil || got.RevisionID == oldRevision || got.Title != "新修订" || got.Content != "😀中文" || !got.Truncated || got.Summary != "当前摘录" || got.SummarySource != "excerpt" {
		t.Fatalf("新修订混用旧增强: %+v %v", got, err)
	}
	// RSS同样读取持久化纯文本，保留来源和原文链接。
	source, _, err := postgres.NewSourceRepository(env.pool).Add(ctx, "https://example.com/agent-feed", "https://example.com/agent-feed", "工具RSS", sourceDomain.DefaultFetchInterval, now)
	if err != nil {
		t.Fatal(err)
	}
	candidate := rssCandidate(source.ID, "RSS正文", "d", now)
	candidate.Content.PlainText = "RSS😀正文"
	_, rssID, err := repo.Upsert(ctx, candidate, now)
	if err != nil {
		t.Fatal(err)
	}
	rss, err := s.Get(ctx, caller, agenttools.GetInput{ArticleID: &rssID, MaxChars: agentPtr(4)})
	if err != nil || rss.Content != "RSS😀" || rss.Source == nil || rss.OriginalURL == nil || rss.RevisionID < 1 {
		t.Fatalf("RSS纯文本: %+v %v", rss, err)
	}
	draft, err := repo.CreateUserArticle(ctx, agentTestUser, revisionFixture("私有草稿", "e"), false, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{agentTestUser, agentTestOther} {
		for _, privateID := range []int64{draft.ID, 99999999} {
			if _, err := s.Get(ctx, agenttools.Caller{UserID: actor}, agenttools.GetInput{ArticleID: &privateID}); agenttools.CodeOf(err) != agenttools.ArticleNotFound {
				t.Fatal("作者/管理员不得越权", err)
			}
		}
	}
	for _, status := range []string{"offline", "deleted"} {
		if _, err := env.pool.Exec(ctx, `UPDATE velis.articles SET status=$2::text,offline_reason='author',offline_at=now(),deleted_at=CASE WHEN $2::text='deleted' THEN now() END WHERE id=$1`, id, status); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(ctx, caller, agenttools.GetInput{ArticleID: &id}); agenttools.CodeOf(err) != agenttools.ArticleNotFound {
			t.Fatal("非公开文章泄露", err)
		}
	}
}

func agentBusinessFacts(t *testing.T, env *testEnv) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, table := range []string{"agent_user_state", "agent_conversations", "agent_messages", "agent_conversation_deletions", "article_read_windows", "article_favorites", "article_not_interested", "articles", "article_versions", "ai_current_selections"} {
		var value string
		if err := env.pool.QueryRow(context.Background(), `SELECT COALESCE(jsonb_agg(v ORDER BY v::text),'[]'::jsonb)::text FROM (SELECT to_jsonb(t) AS v FROM velis.`+table+` t) facts`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		result[table] = value
	}
	return result
}

func agentOpenPITs(t *testing.T) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(os.Getenv("VELIS_TEST_OPENSEARCH_URL"), "/")+"/_search/point_in_time/_all", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var value struct {
		PITs []json.RawMessage `json:"pits"`
	}
	if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&value) != nil {
		t.Fatal("无法读取真实PIT列表")
	}
	return len(value.PITs)
}

func TestAgentToolsPostgresOpenSearchRedisReadOnlyAndRecovery(t *testing.T) {
	env := newTestEnv(t)
	seedAgentUsers(t, env)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Hour)
	ids := []int64{}
	for _, seed := range []string{"a", "b", "c", "d", "e", "f"} {
		ids = append(ids, createPublishedArticle(t, env, agentTestUser, seed, now))
	}
	seedRecommendationLabels(t, env, ids[0], "Go", now)
	seedRecommendationLabels(t, env, ids[1], "Rust", now)
	feedback := postgres.NewArticleFeedbackRepository(env.pool)
	if err := feedback.SetFavorite(ctx, agentTestUser, ids[0], true, now); err != nil {
		t.Fatal(err)
	}
	if err := feedback.SetFavorite(ctx, agentTestOther, ids[1], true, now); err != nil {
		t.Fatal(err)
	}
	if err := feedback.SetNotInterested(ctx, agentTestUser, ids[1], true, time.Now()); err != nil {
		t.Fatal(err)
	}
	stack := newProjectionE2E(t, env, 3)
	stack.drain(t, ctx)
	if err := stack.client.Refresh(ctx, stack.index); err != nil {
		t.Fatal(err)
	}
	h0 := redistest.New(t)
	proxy := redistest.NewProxy(t, h0.Address)
	cache, harness := newOwnedReadCache(t, proxy.Address())
	repo := postgres.NewArticleRepository(env.pool)
	tx := postgres.NewTxManager(env.pool)
	shared := articleread.NewService(repo, tx, cache, nil, 100, nil)
	sc, _ := searchApp.NewCursorCodec([]byte(strings.Repeat("s", 32)))
	search := searchApp.NewService(stack.client, shared, sc, nil, searchApp.Config{Timeout: time.Second, PITKeepAlive: time.Minute, CandidateBatchSize: 100, MaxCandidatesPerRequest: 500}).WithHybrid(nil, shared)
	profile := feedbackApp.NewProfileService(feedback, profileClock{time.Now().UTC()})
	rc, _ := recommendation.NewCursorCodec([]byte(strings.Repeat("r", 32)))
	recConfig := recommendation.Config{FirstQueryTimeout: time.Second, BM25Candidates: 100, KNNCandidates: 100, CursorTTL: 2 * time.Minute}
	recommend := recommendation.NewService(profile, stack.client, shared, rc, nil, recConfig, time.Now).WithPlanCache(cache)
	tools := agenttools.NewService(search, recommend, repo, agenttools.Config{})
	caller := agenttools.Caller{UserID: agentTestUser}
	q := "投影文章"
	baselinePITs := agentOpenPITs(t)
	before := agentBusinessFacts(t, env)
	profilesBefore := make(map[string]recommendation.Profile)
	for _, user := range []string{agentTestUser, agentTestOther} {
		value, err := profile.Profile(ctx, user, ids)
		if err != nil {
			t.Fatal(err)
		}
		profilesBefore[user] = value
	}
	readAll := func() ([]int64, []int64) {
		t.Helper()
		found, err := tools.Search(ctx, caller, agenttools.SearchInput{Q: &q, Limit: agentPtr(2)})
		if err != nil || len(found.Items) != 2 || !found.Truncated {
			t.Fatalf("工具搜索: %+v %v", found, err)
		}
		personal, err := tools.Recommend(ctx, caller, agenttools.RecommendInput{Limit: agentPtr(2)})
		if err != nil || personal.Mode != "personalized" || len(personal.Items) != 2 || personal.Items[0].ArticleID != ids[0] {
			t.Fatalf("工具推荐: %+v %v", personal, err)
		}
		sIDs, rIDs := []int64{}, []int64{}
		for _, ref := range found.Items {
			if ref.RevisionID < 1 {
				t.Fatal("搜索无当前修订")
			}
			sIDs = append(sIDs, ref.ArticleID)
		}
		for _, ref := range personal.Items {
			if ref.ArticleID == ids[1] || ref.RevisionID < 1 {
				t.Fatal("推荐忽略本人排除或修订")
			}
			rIDs = append(rIDs, ref.ArticleID)
		}
		if _, err := tools.Get(ctx, caller, agenttools.GetInput{ArticleID: &ids[0]}); err != nil {
			t.Fatal(err)
		}
		return sIDs, rIDs
	}
	expectedS, expectedR := readAll()
	for _, call := range []func() error{
		func() error {
			query := "绝不匹配的检索词xyz123"
			empty, e := tools.Search(ctx, caller, agenttools.SearchInput{Q: &query})
			if e == nil && len(empty.Items) != 0 {
				return fmt.Errorf("空查询不应使用latest")
			}
			return e
		},
	} {
		if err := call(); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{"warm", "clear", "corrupt", "disconnect", "recover"} {
		t.Run(mode, func(t *testing.T) {
			switch mode {
			case "clear":
				if err := harness.Clear(); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				for _, key := range harness.Keys() {
					if err := harness.Client.Set(ctx, key, "损坏", time.Minute).Err(); err != nil {
						t.Fatal(err)
					}
				}
			case "disconnect":
				proxy.Disconnect(true)
			case "recover":
				proxy.Disconnect(false)
			}
			gotS, gotR := readAll()
			if !reflect.DeepEqual(gotS, expectedS) || !reflect.DeepEqual(gotR, expectedR) {
				t.Fatal("缓存故障改变工具顺序")
			}
		})
	}
	proxy.Disconnect(false)
	other, err := tools.Recommend(ctx, agenttools.Caller{UserID: agentTestOther}, agenttools.RecommendInput{Limit: agentPtr(1)})
	if err != nil || len(other.Items) != 1 || other.Items[0].ArticleID != ids[1] {
		t.Fatalf("工具用户隔离: %+v %v", other, err)
	}
	for n := 0; n < 12; n++ {
		readAll()
	}
	if after := agentOpenPITs(t); after != baselinePITs {
		t.Fatalf("连续调用累积PIT: before=%d after=%d", baselinePITs, after)
	}
	if after := agentBusinessFacts(t, env); !reflect.DeepEqual(before, after) {
		t.Fatal("工具更改了业务事实")
	}
	for user, expected := range profilesBefore {
		actual, err := profile.Profile(ctx, user, ids)
		if err != nil || !reflect.DeepEqual(actual, expected) {
			t.Fatal("工具更改了本人画像", err)
		}
	}
	// 暖缓存后改变当前修订并下架另一文章；旧索引仍存在时不能返回陈旧卡片。
	updated, _, err := repo.UpdateUserRevision(ctx, ids[0], agentTestUser, 1, articleDomain.RevisionData{Title: "当前工具修订", PlainText: "当前正文", Excerpt: "当前摘录", Language: "zh-CN", ContentHash: strings.Repeat("9", 64), SanitizerVersion: 1}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE velis.articles SET status='offline',offline_reason='author',offline_at=now() WHERE id=$1`, ids[2]); err != nil {
		t.Fatal(err)
	}
	found, err := tools.Search(ctx, caller, agenttools.SearchInput{Q: &q, Limit: agentPtr(10)})
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range found.Items {
		if ref.ArticleID == ids[2] {
			t.Fatal("陈旧索引泄露下架文章")
		}
		if ref.ArticleID == ids[0] && (ref.Title != "当前工具修订" || ref.RevisionID != updated.RevisionID) {
			t.Fatal("新旧修订卡片混合")
		}
	}
	// 搜索故障必须报错，推荐沿用latest回退；不用停止共享服务。
	if _, err := env.pool.Exec(ctx, `DELETE FROM velis.ai_current_selections WHERE article_id=$1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	seedRecommendationLabels(t, env, ids[0], "Go", time.Now())
	unavailable := searchApp.UnavailableService{}
	fallbackRec := recommendation.NewService(profile, nil, shared, rc, nil, recConfig, time.Now)
	fallbackTools := agenttools.NewService(unavailable, fallbackRec, repo, agenttools.Config{})
	if _, err := fallbackTools.Search(ctx, caller, agenttools.SearchInput{Q: &q}); agenttools.CodeOf(err) != agenttools.SearchUnavailable {
		t.Fatal("搜索故障伪装空结果", err)
	}
	fallback, err := fallbackTools.Recommend(ctx, caller, agenttools.RecommendInput{})
	if err != nil || fallback.Mode != "latest_fallback" || !fallback.Degraded {
		t.Fatalf("推荐回退: %+v %v", fallback, err)
	}
	// 只有负反馈的账户仍是cold_start，且不得产生正向信号。
	if _, err := env.pool.Exec(ctx, `DELETE FROM velis.article_favorites WHERE user_id=$1`, agentTestOther); err != nil {
		t.Fatal(err)
	}
	if err := feedback.SetNotInterested(ctx, agentTestOther, ids[3], true, time.Now()); err != nil {
		t.Fatal(err)
	}
	cold, err := tools.Recommend(ctx, agenttools.Caller{UserID: agentTestOther}, agenttools.RecommendInput{})
	if err != nil || cold.Mode != "cold_start" {
		t.Fatalf("仅负反馈冷启动: %+v %v", cold, err)
	}
	for _, ref := range cold.Items {
		if ref.ArticleID == ids[3] {
			t.Fatal("冷启动忽略排除")
		}
	}
	// 有效缓存也不能掩盖PG断连；使用自有代理，不操作真实服务状态。
	pgConfig := env.pool.Config().Copy()
	pgProxy := redistest.NewProxy(t, net.JoinHostPort(pgConfig.ConnConfig.Host, strconv.Itoa(int(pgConfig.ConnConfig.Port))))
	host, port, err := net.SplitHostPort(pgProxy.Address())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	pgConfig.ConnConfig.Host = host
	pgConfig.ConnConfig.Port = uint16(parsed)
	pgConfig.ConnConfig.Fallbacks = nil
	pgConfig.ConnConfig.ConnectTimeout = 100 * time.Millisecond
	brokenPool, err := pgxpool.NewWithConfig(ctx, pgConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(brokenPool.Close)
	brokenRepo := postgres.NewArticleRepository(brokenPool)
	brokenShared := articleread.NewService(brokenRepo, postgres.NewTxManager(brokenPool), cache, nil, 100, nil)
	if _, err := brokenShared.ListPublishedWithIdentity(ctx, ids); err != nil {
		t.Fatal(err)
	}
	brokenSearch := searchApp.NewService(stack.client, brokenShared, sc, nil, searchApp.Config{Timeout: time.Second, PITKeepAlive: time.Minute, CandidateBatchSize: 100, MaxCandidatesPerRequest: 500}).WithHybrid(nil, brokenShared)
	brokenProfile := feedbackApp.NewProfileService(postgres.NewArticleFeedbackRepository(brokenPool), profileClock{time.Now().UTC()})
	brokenRec := recommendation.NewService(brokenProfile, stack.client, brokenShared, rc, nil, recConfig, time.Now).WithPlanCache(cache)
	brokenTools := agenttools.NewService(brokenSearch, brokenRec, brokenRepo, agenttools.Config{})
	pgProxy.Disconnect(true)
	for _, call := range []func() error{
		func() error { _, e := brokenTools.Search(ctx, caller, agenttools.SearchInput{Q: &q}); return e },
		func() error { _, e := brokenTools.Recommend(ctx, caller, agenttools.RecommendInput{}); return e },
		func() error { _, e := brokenTools.Get(ctx, caller, agenttools.GetInput{ArticleID: &ids[0]}); return e },
	} {
		if err := call(); agenttools.CodeOf(err) != agenttools.DependencyUnavailable {
			t.Fatal("暖缓存掩盖PG/排除集故障", err)
		}
	}
	pgProxy.Disconnect(false)
}

func TestAgentToolsConcurrentRevisionProjection(t *testing.T) {
	env := newTestEnv(t)
	seedAgentUsers(t, env)
	ctx := context.Background()
	repo := postgres.NewArticleRepository(env.pool)
	initial := revisionFixture("版本1", "a")
	initial.PlainText = "版本1"
	initial.Excerpt = "版本1"
	created, err := repo.CreateUserArticle(ctx, agentTestUser, initial, true, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	shared := articleread.NewService(repo, postgres.NewTxManager(env.pool), nil, nil, 100, nil)
	if os.Getenv("VELIS_TEST_REDIS_ADDRESS") != "" {
		cache, _ := newOwnedReadCache(t, "")
		shared = articleread.NewService(repo, postgres.NewTxManager(env.pool), cache, nil, 100, nil)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	wg.Go(func() {
		for n := 2; n <= 40; n++ {
			name := fmt.Sprintf("版本%d", n)
			_, changed, err := repo.UpdateUserRevision(ctx, created.ID, agentTestUser, int64(n-1), articleDomain.RevisionData{Title: name, PlainText: name, Excerpt: name, Language: "zh-CN", ContentHash: fmt.Sprintf("%064x", n), SanitizerVersion: 1}, time.Now())
			if err != nil || !changed {
				failures <- fmt.Errorf("并发修订: %v", err)
				return
			}
		}
	})
	for worker := 0; worker < 3; worker++ {
		wg.Go(func() {
			for n := 0; n < 50; n++ {
				items, err := shared.ListPublishedWithIdentity(ctx, []int64{created.ID})
				if err != nil || len(items) != 1 {
					failures <- fmt.Errorf("批量投影: %v", err)
					return
				}
				item := items[0]
				version, err := strconv.Atoi(strings.TrimPrefix(item.Item.Title, "版本"))
				if err != nil || item.Identity.RevisionID != created.RevisionID+int64(version-1) || item.Item.RevisionID != item.Identity.RevisionID || item.Item.Excerpt != item.Item.Title {
					failures <- fmt.Errorf("卡片/身份混合: %+v", item)
					return
				}
				text, err := repo.ReadPublicText(ctx, created.ID, 1000)
				if err != nil || text.Item.Title != text.Content || text.Item.Excerpt != text.Content {
					failures <- fmt.Errorf("正文/修订混合: %+v %v", text, err)
					return
				}
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
}

type agentCancelIndex struct {
	searchApp.QueryIndex
	cancel context.CancelFunc
}

func (i agentCancelIndex) Search(ctx context.Context, request searchApp.IndexRequest) (searchApp.CandidateBatch, error) {
	i.cancel()
	return i.QueryIndex.Search(ctx, request)
}

func TestAgentToolsHybridAndCanceledSearchReleaseRealPIT(t *testing.T) {
	env := newTestEnv(t)
	seedAgentUsers(t, env)
	ctx := context.Background()
	for _, seed := range []string{"a", "b", "c"} {
		createPublishedArticle(t, env, agentTestUser, seed, time.Now().Add(-time.Hour))
	}
	stack := newProjectionE2E(t, env, 3)
	stack.drain(t, ctx)
	rebuild := projectionApp.NewRebuildService(stack.repository, stack.repository, stack.client, stack.client, stack.repository, projectionApp.RebuildPolicy{
		IndexPrefix: stack.prefix, SchemaVersion: 2, SchemaIdentity: "mapping-v2|analyzer-cjk|dims-3|encoding-v2", EmbeddingDimensions: 3, SnapshotBatch: 10, RollbackWindow: time.Hour, Lease: 2 * time.Second, SampleSize: 10, PollInterval: 20 * time.Millisecond,
	}, nil)
	state, err := rebuild.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stack.client.Delete(context.Background(), state.CandidateIndex) })
	if _, err := rebuild.Cutover(ctx, state.ID); err != nil {
		t.Fatal(err)
	}
	if err := stack.client.Refresh(ctx, state.CandidateIndex); err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewArticleRepository(env.pool)
	reader := sharedIntegrationReader(t, env)
	codec, _ := searchApp.NewCursorCodec([]byte(strings.Repeat("h", 32)))
	cfg := searchApp.Config{Timeout: time.Second, PITKeepAlive: time.Minute, CandidateBatchSize: 100, MaxCandidatesPerRequest: 500, Hybrid: searchApp.HybridConfig{Enabled: true, BM25Candidates: 100, KNNCandidates: 100, EmbeddingTimeout: time.Second, KNNTimeout: time.Second, Dimensions: 3, Profile: "e-v1"}}
	hybrid := searchApp.NewService(stack.client, reader, codec, nil, cfg).WithHybrid(recommendationVector{}, reader)
	tools := agenttools.NewService(hybrid, nil, repo, agenttools.Config{})
	baseline := agentOpenPITs(t)
	query := "投影文章"
	for n := 0; n < 3; n++ {
		found, err := tools.Search(ctx, agenttools.Caller{UserID: agentTestUser}, agenttools.SearchInput{Q: &query, Limit: agentPtr(1)})
		if err != nil || len(found.Items) != 1 || !found.Truncated {
			t.Fatalf("混合无向量降级: %+v %v", found, err)
		}
		if agentOpenPITs(t) != baseline {
			t.Fatal("混合一次检索遗留PIT")
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	index := agentCancelIndex{QueryIndex: stack.client, cancel: cancel}
	once := searchApp.NewService(index, reader, codec, nil, searchApp.Config{Timeout: time.Second, PITKeepAlive: time.Minute, CandidateBatchSize: 100, MaxCandidatesPerRequest: 500}).WithHybrid(nil, reader)
	_, err = agenttools.NewService(once, nil, repo, agenttools.Config{}).Search(canceled, agenttools.Caller{UserID: agentTestUser}, agenttools.SearchInput{Q: &query})
	if agenttools.CodeOf(err) != agenttools.ToolCanceled {
		t.Fatal("主动取消错误", err)
	}
	if agentOpenPITs(t) != baseline {
		t.Fatal("真实取消后PIT遗留")
	}
}
