package integration

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	amqp "github.com/rabbitmq/amqp091-go"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asynctask"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	relayApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/relay"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
	einoAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/ai/eino"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/fetcher/httpfeed"
	rabbitAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/messaging/rabbitmq"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

type longRSSE2EChatModel struct {
	healthy bool
	mu      sync.Mutex
	counts  map[string]int
}

func (m *longRSSE2EChatModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	prompt := input[len(input)-1].Content
	kind := "generation_single"
	if strings.Contains(prompt, "任务: MAP_CHUNK") {
		kind = "generation_map"
	} else if strings.Contains(prompt, "任务: REPAIR_FINAL_JSON") {
		kind = "generation_repair"
	} else if strings.Contains(prompt, `"title":"RSS 长文`) || strings.Contains(prompt, `"title":"RSS 失败恢复"`) {
		kind = "generation_reduce"
	}
	m.mu.Lock()
	m.counts[kind]++
	m.mu.Unlock()

	if kind == "generation_map" {
		return schema.AssistantMessage("RSS 分块事实摘要", nil), nil
	}
	if !m.healthy && strings.Contains(prompt, `"title":"RSS 失败恢复"`) {
		return nil, errors.New("可控 Provider 失败")
	}
	if !m.healthy && strings.Contains(prompt, `"title":"RSS 长文纠正"`) {
		return schema.AssistantMessage("需要纠正的非 JSON 输出", nil), nil
	}
	return schema.AssistantMessage(`{"summary":"RSS AI 摘要","keywords":["RSS","Eino"],"topics":["内容增强"]}`, nil), nil
}

func (*longRSSE2EChatModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("未使用流式生成")
}

func (m *longRSSE2EChatModel) count(kind string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[kind]
}

type dimensions1024Embedder struct{}

func (dimensions1024Embedder) EmbedStrings(context.Context, []string, ...embedding.Option) ([][]float64, error) {
	vector := make([]float64, 1024)
	for index := range vector {
		vector[index] = float64(index+1) / 1024
	}
	return [][]float64{vector}, nil
}

func TestLongRSSAIEnrichmentAndBoundedRecoveryE2E(t *testing.T) {
	rabbitURL := os.Getenv("VELIS_TEST_RABBITMQ_URL")
	if rabbitURL == "" {
		t.Skip("未设置 VELIS_TEST_RABBITMQ_URL")
	}
	env := newTestEnv(t)
	env.resetArticles(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	purgeAIQueues(t, rabbitURL)

	now := fixedNow()
	source, inserted, err := postgres.NewSourceRepository(env.pool).Add(ctx, "https://ai-rss.example/feed", "https://ai-rss.example/feed", "AI RSS E2E", sourceDomain.DefaultFetchInterval, now)
	if err != nil || !inserted {
		t.Fatalf("创建 RSS Source: inserted=%t err=%v", inserted, err)
	}
	shortID, repairID, failureID := "rss-short", "rss-long-repair", "rss-long-failure"
	shortBody := "这是一篇用于验证 single 路径的短 RSS 正文。"
	longBody := strings.Repeat("这是需要进行分块归纳的长 RSS 正文，包含稳定事实。", 24)
	service := articleApp.NewServiceWithOutbox(postgres.NewArticleRepository(env.pool), httpfeed.NewSanitizer(), &articleTestClock{now: now}, postgres.NewTxManager(env.pool), postgres.NewOutboxRepository(env.pool))
	report, err := service.Ingest(ctx, source.ID, []ports.ParsedItem{
		{ID: &shortID, URL: "https://ai-rss.example/short", Title: "RSS 短文", Content: &shortBody, Language: "zh-CN"},
		{ID: &repairID, URL: "https://ai-rss.example/repair", Title: "RSS 长文纠正", Content: &longBody, Language: "zh-CN"},
		{ID: &failureID, URL: "https://ai-rss.example/failure", Title: "RSS 失败恢复", Content: &longBody, Language: "zh-CN"},
	})
	if err != nil || report.Inserted != 3 {
		t.Fatalf("写入 RSS: report=%+v err=%v", report, err)
	}
	publishAndProjectAIEvents(t, ctx, env, rabbitURL, 3)

	initialModel := &longRSSE2EChatModel{counts: make(map[string]int)}
	initialExecutor := newRSSAIExecutor(t, ctx, env, initialModel, "generation-v2", 1)
	processAIUntilIdle(t, ctx, initialExecutor, "rss-ai-initial", 12)
	if initialModel.count("generation_single") != 1 || initialModel.count("generation_map") == 0 || initialModel.count("generation_reduce") != 2 || initialModel.count("generation_repair") != 1 {
		t.Fatalf("初始调用路径不完整: single=%d map=%d reduce=%d repair=%d", initialModel.count("generation_single"), initialModel.count("generation_map"), initialModel.count("generation_reduce"), initialModel.count("generation_repair"))
	}
	var failed, repairCalls int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.async_tasks WHERE status='failed'`).Scan(&failed); err != nil || failed != 1 {
		t.Fatalf("应有一个可恢复失败任务: failed=%d err=%v", failed, err)
	}
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.ai_model_calls WHERE call_kind='generation_repair'`).Scan(&repairCalls); err != nil || repairCalls != 1 {
		t.Fatalf("全部初始任务总共应只有一次纠正调用: calls=%d err=%v", repairCalls, err)
	}

	backfill := enrichment.NewAIBackfillService(postgres.NewEnrichmentBackfillRepository(env.pool))
	missing, err := backfill.Run(ctx, enrichment.BackfillRequest{Stage: "generation", Mode: "missing-only", Limit: 1, GenerationProfile: "generation-v2-recovery"})
	if err != nil || missing.Created != 1 || missing.HasMore {
		t.Fatalf("missing-only 有界恢复: report=%+v err=%v", missing, err)
	}
	healthyModel := &longRSSE2EChatModel{healthy: true, counts: make(map[string]int)}
	recoveryExecutor := newRSSAIExecutor(t, ctx, env, healthyModel, "generation-v2-recovery", 1)
	processAIUntilIdle(t, ctx, recoveryExecutor, "rss-ai-missing", 4)

	outdated, err := backfill.Run(ctx, enrichment.BackfillRequest{Stage: "generation", Mode: "outdated-only", Limit: 1, GenerationProfile: "generation-v3"})
	if err != nil || outdated.Created != 1 || !outdated.HasMore {
		t.Fatalf("outdated-only 应限量推进并报告剩余项: report=%+v err=%v", outdated, err)
	}
	outdatedExecutor := newRSSAIExecutor(t, ctx, env, healthyModel, "generation-v3", 1)
	processAIUntilIdle(t, ctx, outdatedExecutor, "rss-ai-outdated", 4)

	var selected, dimensions1024 int
	if err := env.pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE e.dimensions=1024 AND cardinality(e.vector)=1024)
FROM velis.ai_current_selections s JOIN velis.ai_embedding_results e ON e.id=s.embedding_result_id
JOIN velis.articles a ON a.id=s.article_id AND a.current_revision_id=s.revision_id WHERE a.source_id=$1`, source.ID).Scan(&selected, &dimensions1024); err != nil {
		t.Fatal(err)
	}
	if selected != 3 || dimensions1024 != 3 {
		t.Fatalf("RSS 当前向量不完整: selected=%d dimensions1024=%d", selected, dimensions1024)
	}
}

func purgeAIQueues(t *testing.T, rabbitURL string) {
	t.Helper()
	connection, err := amqp.Dial(rabbitURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	if err := rabbitAdapter.DeclareTopology(channel); err != nil {
		t.Fatal(err)
	}
	if _, err := channel.QueuePurge(rabbitAdapter.ConsumerQueue, false); err != nil {
		t.Fatal(err)
	}
	if _, err := channel.QueuePurge(rabbitAdapter.DeadQueue, false); err != nil {
		t.Fatal(err)
	}
}

func publishAndProjectAIEvents(t *testing.T, ctx context.Context, env *testEnv, rabbitURL string, expected int) {
	t.Helper()
	publisher, err := rabbitAdapter.DialPublisher(rabbitURL)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	relay := relayApp.NewService(postgres.NewRelayRepository(env.pool), publisher, relayApp.Config{Owner: "rss-ai-relay", BatchSize: 10, PublishWindow: 3, Lease: time.Minute, ConfirmTimeout: 3 * time.Second, BackoffMin: time.Second, BackoffMax: time.Minute})
	if err := relay.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	inbox, err := postgres.NewConsumedEventRepository(asynctask.ConsumerName)
	if err != nil {
		t.Fatal(err)
	}
	projector := asynctask.NewService(postgres.NewTxManager(env.pool), inbox, postgres.NewAsyncTaskRepository(), time.Now)
	consumerCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- rabbitAdapter.NewConsumer(rabbitURL, 16, projector, func(error) bool { return false }).Run(consumerCtx)
	}()
	for ctx.Err() == nil {
		var count int
		if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.async_tasks`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == expected {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatal("等待 RabbitMQ 投影 RSS 增强任务超时")
	}
}

func newRSSAIExecutor(t *testing.T, ctx context.Context, env *testEnv, chat model.BaseChatModel, generationProfile string, maxAttempts int) *enrichment.Executor {
	t.Helper()
	workflow, err := einoAdapter.NewWorkflow(ctx, chat)
	if err != nil {
		t.Fatal(err)
	}
	generation := &enrichment.ActiveProfile{Provider: "deterministic", Model: "chat-stub", ProfileVersion: generationProfile, WorkflowVersion: "hierarchical-v2", PromptVersion: "summary-v2", StructuredOutput: "prompt", MaxAttempts: maxAttempts, AuditTokenBudget: 20000, StageBudget: 3 * time.Second}
	embeddingProfile := &enrichment.ActiveProfile{Provider: "deterministic", Model: "embedding-stub", ProfileVersion: "embedding-v2", InputVersion: "retrieval-document-v1", Dimensions: 1024, MaxAttempts: 1, AuditTokenBudget: 10000, StageBudget: 2 * time.Second}
	policy := enrichment.ExecutorPolicy{Lease: time.Minute, GenerationBackoffMin: time.Millisecond, GenerationBackoffMax: time.Millisecond, EmbeddingBackoffMin: time.Millisecond, EmbeddingBackoffMax: time.Millisecond, ChunkChars: 120, MaxChunks: 8, SingleInputChars: 80, MaxCalls: 9, Concurrency: 2, MapSummaryChars: 800, RepairInputChars: 16000, MaxOutputTokens: 1200, OutputLimits: enrichment.OutputLimits{SummaryChars: 1000, KeywordCount: 12, TopicCount: 5, LabelChars: 64}, EmbeddingBodyChars: 12000}
	return enrichment.NewExecutor(postgres.NewEnrichmentRepository(env.pool), workflow, workflow, einoAdapter.NewEmbeddingAdapter(dimensions1024Embedder{}, 1024), generation, embeddingProfile, policy, nil, nil)
}

func processAIUntilIdle(t *testing.T, ctx context.Context, executor *enrichment.Executor, owner string, max int) {
	t.Helper()
	for range max {
		processed, err := executor.ProcessOne(ctx, owner)
		if err != nil {
			t.Fatal(err)
		}
		if !processed {
			return
		}
	}
	t.Fatalf("AI Executor 在 %d 次处理后仍未空闲", max)
}
