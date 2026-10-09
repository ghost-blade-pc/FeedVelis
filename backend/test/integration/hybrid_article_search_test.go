package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/common/ut"
	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	einoAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/ai/eino"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
	searchAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/search/opensearch"
	hertzhttp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/dto"
)

type failedKNNIndex struct{ *searchAdapter.Client }

func (i failedKNNIndex) SearchKNN(context.Context, searchApp.KNNRequest) (searchApp.CandidateBatch, error) {
	return searchApp.CandidateBatch{}, errors.New("确定性 KNN 故障")
}

func TestHybridArticleSearchHTTPRebuildAndRollback(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	ctx := context.Background()
	g, e := testGenerationID, testEmbeddingID
	insertGenerationResult(t, env, g, "p1", 7101, 7111)
	insertEmbeddingResult(t, env, e, g, 7101, 7111)
	setCurrentSelection(t, env, 7101, 7111, &g, &e)
	if _, err := env.pool.Exec(ctx, `UPDATE velis.article_versions SET title=CASE id WHEN 7111 THEN 'distributed storage' ELSE '缓存 中文 Go' END,plain_text=CASE id WHEN 7111 THEN 'distributed storage' ELSE '缓存 中文 Go' END WHERE id IN(7111,7112)`); err != nil {
		t.Fatal(err)
	}
	stack := newRebuildStack(t, env, 3)
	if err := postgres.NewTxManager(env.pool).WithinTransaction(ctx, func(txCtx context.Context) error {
		for _, id := range []int64{7101, 7102} {
			if _, err := stack.repository.Advance(txCtx, id, time.Now()); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	initial, err := stack.service.InitIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	executor := projectionApp.NewExecutor(stack.repository, stack.repository, stack.repository, stack.client, projectionApp.ExecutorPolicy{Lease: time.Minute, BatchSize: 20, MaxAttempts: 3, BackoffMin: time.Second, BackoffMax: time.Minute, SchemaVersion: 1, Owner: "hybrid-e2e"}, nil, nil)
	if _, err = executor.ProcessBatch(ctx); err != nil {
		t.Fatal(err)
	}
	if err = stack.client.Refresh(ctx, initial.PhysicalIndex); err != nil {
		t.Fatal(err)
	}
	var modelCalls atomic.Int32
	var modelFailure atomic.Bool
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		if modelFailure.Load() {
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited","type":"limit"}}`))
			return
		}
		var input struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if len(input.Input) != 1 || input.Input[0] != "缓存" {
			t.Errorf("在线输入: %+v", input)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float64{0.25, 0.5, 0.75}}}})
	}))
	defer model.Close()
	cfg := config.Default().AI.Embedding
	cfg.Dimensions = 3
	cfg.Profile.BaseURL = model.URL
	cfg.Profile.APIKey = "test"
	cfg.Profile.Model = "fixture"
	embed, err := einoAdapter.NewOpenAIQueryEmbedder(ctx, cfg, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	codec, _ := searchApp.NewCursorCodec([]byte(strings.Repeat("k", 32)))
	policy := searchApp.Config{Timeout: 5 * time.Second, PITKeepAlive: time.Minute, CandidateBatchSize: 100, MaxCandidatesPerRequest: 500, Hybrid: searchApp.HybridConfig{Enabled: true, BM25Candidates: 100, KNNCandidates: 100, EmbeddingTimeout: time.Second, KNNTimeout: time.Second, Dimensions: 3, Profile: "e-v1"}}
	var cacheFault func(bool)
	reader := sharedIntegrationReader(t, env, &cacheFault)
	makeService := func(index searchApp.QueryIndex) *searchApp.Service {
		return searchApp.NewService(index, reader, codec, nil, policy).WithHybrid(embed, reader)
	}
	service := makeService(stack.client)
	server := hertzhttp.NewServer(hertzhttp.Options{Address: "127.0.0.1:0", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Search: service})
	request := func(cursor string, limit int) dto.ArticleSearchPage {
		t.Helper()
		path := "/api/v1/search/articles?q=" + url.QueryEscape("缓存") + "&limit=1"
		if limit == 50 {
			path = "/api/v1/search/articles?q=" + url.QueryEscape("缓存") + "&limit=50"
		}
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		response := ut.PerformRequest(server.Engine, "GET", path, nil)
		if response.Code != 200 {
			t.Fatalf("HTTP 搜索: %d %s", response.Code, response.Body.String())
		}
		var page dto.ArticleSearchPage
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	old := request("", 50)
	if len(old.Items) != 1 || old.Items[0].ID != 7102 || modelCalls.Load() != 0 {
		t.Fatalf("v1 应保留文本且跳过模型: %+v", old)
	}
	rebuild := projectionApp.NewRebuildService(stack.repository, stack.repository, stack.client, stack.client, stack.repository, projectionApp.RebuildPolicy{IndexPrefix: stack.prefix, SchemaVersion: 2, SchemaIdentity: "mapping-v2|analyzer-cjk|dims-3|encoding-v2", EmbeddingDimensions: 3, SnapshotBatch: 2, RollbackWindow: time.Hour, Lease: 2 * time.Second, SampleSize: 20, PollInterval: 20 * time.Millisecond}, nil)
	state, err := rebuild.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Validation == nil || !state.Validation.Passed() {
		t.Fatalf("v2 重建校验失败: %+v", state)
	}
	if _, err = rebuild.Cutover(ctx, state.ID); err != nil {
		t.Fatal(err)
	}
	first := request("", 1)
	if len(first.Items) != 1 || !first.HasMore || first.NextCursor == nil {
		t.Fatalf("混合第一页: %+v", first)
	}
	// 同一个 v2 投影在回滚窗口内分别编码到 strict v1/v2。
	projections, err := stack.repository.Current(ctx, []int64{7101})
	if err != nil {
		t.Fatal(err)
	}
	document := projections[0].Document
	document.SchemaVersion = 2
	document.Generation = 2
	document.ContentHash = projectionApp.DocumentFingerprint(document)
	outcomes, err := stack.client.Bulk(ctx, []projectionApp.BulkItem{{PhysicalIndex: initial.PhysicalIndex, Document: document}, {PhysicalIndex: state.CandidateIndex, Document: document}})
	if err != nil {
		t.Fatal(err)
	}
	for _, outcome := range outcomes {
		if !outcome.Result.Applied() {
			t.Fatalf("跨版本双写失败: %+v", outcome)
		}
	}
	oldRaw, err := stack.client.RawDocument(ctx, initial.PhysicalIndex, 7101)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := oldRaw["embedding_profile_version"]; ok {
		t.Fatal("v1 strict 含 profile")
	}
	newRaw, err := stack.client.RawDocument(ctx, state.CandidateIndex, 7101)
	if err != nil || newRaw["embedding_profile_version"] != "e-v1" {
		t.Fatalf("v2 actual profile: %+v %v", newRaw, err)
	}
	if _, err = rebuild.Rollback(ctx, state.ID); err != nil {
		t.Fatal(err)
	}
	persisted, err := stack.repository.State(ctx)
	if err != nil || persisted.SchemaVersion != 1 || persisted.SchemaIdentity != initial.SchemaIdentity {
		t.Fatalf("回滚 metadata 错误: %+v %v", persisted, err)
	}
	before := modelCalls.Load()
	second := request(*first.NextCursor, 50)
	if len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID || second.HasMore || modelCalls.Load() != before {
		t.Fatalf("回滚改变冻结页: %+v", second)
	}
	afterRollback := request("", 50)
	if len(afterRollback.Items) != 1 || modelCalls.Load() != before {
		t.Fatalf("v1 新查询应 BM25: %+v", afterRollback)
	}
	// 再切到已验证 v2，分别注入模型和 KNN 故障，HTTP 应保持正常 BM25。
	if err = stack.client.SwitchAliases(ctx, projectionApp.AliasSwitch{Index: state.CandidateIndex, DetachIndex: initial.PhysicalIndex, ReadAlias: stack.prefix + "-read", WriteAlias: stack.prefix + "-write"}); err != nil {
		t.Fatal(err)
	}
	modelFailure.Store(true)
	degraded := request("", 50)
	if len(degraded.Items) != 1 || degraded.Items[0].ID != 7102 {
		t.Fatalf("模型故障: %+v", degraded)
	}
	modelFailure.Store(false)
	failedService := makeService(failedKNNIndex{stack.client})
	page, err := failedService.Search(ctx, searchApp.Request{Q: "缓存", Limit: 50})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != 7102 {
		t.Fatalf("KNN 故障: %+v %v", page, err)
	}
	failedServer := hertzhttp.NewServer(hertzhttp.Options{Address: "127.0.0.1:0", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Search: failedService})
	failedHTTP := ut.PerformRequest(failedServer.Engine, "GET", "/api/v1/search/articles?q="+url.QueryEscape("缓存"), nil)
	if failedHTTP.Code != 200 || !strings.Contains(failedHTTP.Body.String(), `"id":7102`) {
		t.Fatalf("KNN 故障 HTTP 失败: %d", failedHTTP.Code)
	}
	// 相同固定集合重复首查产生相同次序。
	cacheFault(true)
	defer cacheFault(false)
	a, b := request("", 50), request("", 50)
	if len(a.Items) != 2 || len(b.Items) != 2 || a.Items[0].ID != b.Items[0].ID || a.Items[1].ID != b.Items[1].ID {
		t.Fatalf("重复查询不稳定: %+v %+v", a, b)
	}
}
