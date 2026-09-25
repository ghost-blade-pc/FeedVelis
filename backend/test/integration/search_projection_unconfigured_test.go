package integration

import (
	"context"
	"strconv"
	"testing"
	"time"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	enrichmentApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	einoAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/ai/eino"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

// TestSearchProjectionUnconfiguredKeepsArticleAndAIPathsWorking 覆盖 3.4：
// 未配置 OpenSearch 与 RabbitMQ 时，文章与 AI 写路径只生成 PostgreSQL 待处理槽位，
// 发布、latest、详情与增强执行都保持可用。
func TestSearchProjectionUnconfiguredKeepsArticleAndAIPathsWorking(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	ctx := context.Background()
	now := fixedNow()
	// 不建立索引服务状态：等价于 OpenSearch 未配置或尚未初始化。
	const authorID = "95000000-0000-0000-0000-000000000001"
	seedIntegrationUser(t, env, authorID, "unconfigured_author", "user", now)
	repository := postgres.NewArticleRepository(env.pool)
	service := newUserArticleStack(t, env, now)

	published, _, err := service.Create(ctx, articleApp.CreateUserArticleCommand{
		AuthorUserID: authorID, IdempotencyKey: "95000000-0000-0000-0000-000000000011",
		Title: "无索引文章", Markdown: "无索引正文", InitialStatus: articleDomain.StatusPublished})
	if err != nil || published.Article.Status != articleDomain.StatusPublished {
		t.Fatalf("发布文章: %+v err=%v", published.Article, err)
	}
	job, ok := readProjectionJob(t, env, published.Article.ID)
	if !ok || job.Action != "upsert" || job.Status != "pending" || job.Generation != 1 {
		t.Fatalf("必须只留下待处理的 upsert 槽位: %+v", job)
	}
	assertDeliveryCount(t, env, published.Article.ID, 0)

	// 详情与 latest 不依赖 OpenSearch。
	detail, err := repository.GetPublished(ctx, published.Article.ID)
	if err != nil || detail.Item.Title != "无索引文章" {
		t.Fatalf("详情读取: %+v err=%v", detail.Item, err)
	}
	items, err := repository.ListPublished(ctx, nil, 20)
	if err != nil || len(items) != 1 || items[0].ID != published.Article.ID {
		t.Fatalf("latest 列表: %+v err=%v", items, err)
	}
	// 未配置 Relay 时事件必须留待发布，而不是丢失。
	var events int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM velis.outbox_events
WHERE aggregate_id=$1 AND published_at IS NULL`, strconv.FormatInt(published.Article.ID, 10)).Scan(&events); err != nil || events != 1 {
		t.Fatalf("待发布 Outbox 事件数 = %d err=%v", events, err)
	}

	// AI 增强执行在无 OpenSearch 时照常完成，只推进本地槽位。
	seedAIUpgradeTasks(t, env)
	workflow, err := einoAdapter.NewWorkflow(ctx, e2eChatModel{})
	if err != nil {
		t.Fatal(err)
	}
	gen := &enrichmentApp.ActiveProfile{Provider: "deterministic", Model: "chat-stub", ProfileVersion: "g-unconfigured",
		WorkflowVersion: "w1", PromptVersion: "p1", StructuredOutput: "prompt", MaxAttempts: 2, AuditTokenBudget: 1000, StageBudget: time.Second}
	embed := &enrichmentApp.ActiveProfile{Provider: "deterministic", Model: "embed-stub", ProfileVersion: "e-unconfigured",
		InputVersion: "i1", Dimensions: 3, MaxAttempts: 2, AuditTokenBudget: 1000, StageBudget: time.Second}
	executor := enrichmentApp.NewExecutor(postgres.NewEnrichmentRepository(env.pool), workflow, workflow,
		einoAdapter.NewEmbeddingAdapter(e2eEmbedder{}, 3), gen, embed, enrichmentApp.ExecutorPolicy{
			Lease: time.Minute, GenerationBackoffMin: time.Second, GenerationBackoffMax: time.Minute,
			EmbeddingBackoffMin: time.Second, EmbeddingBackoffMax: time.Minute, ChunkChars: 1000, MaxChunks: 8,
			SingleInputChars: 12000, MaxCalls: 9, Concurrency: 2, MapSummaryChars: 800, RepairInputChars: 16000,
			MaxOutputTokens: 1000, OutputLimits: enrichmentApp.OutputLimits{SummaryChars: 1000, KeywordCount: 12, TopicCount: 5, LabelChars: 64},
			EmbeddingBodyChars: 12000}, nil, nil)
	for range 2 {
		processed, runErr := executor.ProcessOne(ctx, "unconfigured-worker")
		if runErr != nil || !processed {
			t.Fatalf("增强执行 processed=%t err=%v", processed, runErr)
		}
	}
	aiJob, ok := readProjectionJob(t, env, 7101)
	if !ok || aiJob.Generation != 2 || aiJob.Status != "pending" || aiJob.EmbeddingResultID == nil {
		t.Fatalf("AI 切换必须只推进本地待处理槽位: %+v", aiJob)
	}
	assertDeliveryCount(t, env, 7101, 0)
	// 增强结果本身照常可读。
	enhanced, err := repository.GetPublished(ctx, 7101)
	if err != nil || enhanced.Item.Enhancement == nil || enhanced.Item.Enhancement.Summary != "E2E AI 摘要" {
		t.Fatalf("增强结果读取: %+v err=%v", enhanced.Item, err)
	}
}

func assertDeliveryCount(t *testing.T, env *testEnv, articleID int64, want int) {
	t.Helper()
	var count int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM velis.search_projection_deliveries WHERE article_id=$1`, articleID).Scan(&count); err != nil || count != want {
		t.Fatalf("delivery 数量 = %d，期望 %d，err=%v", count, want, err)
	}
}
