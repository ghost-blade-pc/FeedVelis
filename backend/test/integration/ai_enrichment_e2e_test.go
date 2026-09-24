package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
	einoAdapter "github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/ai/eino"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/persistence/postgres"
)

type e2eChatModel struct{}

func (e2eChatModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	prompt := input[len(input)-1].Content
	if strings.Contains(prompt, "MAP_CHUNK") {
		return schema.AssistantMessage("分块摘要", nil), nil
	}
	return schema.AssistantMessage(`{"summary":"E2E AI 摘要","keywords":["Eino"],"topics":["内容增强"]}`, nil), nil
}
func (e2eChatModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("未使用")
}

type e2eEmbedder struct{}

func (e2eEmbedder) EmbedStrings(context.Context, []string, ...embedding.Option) ([][]float64, error) {
	return [][]float64{{0.1, 0.2, 0.3}}, nil
}

func TestDeterministicAIEnrichmentE2E(t *testing.T) {
	env := newTestEnv(t)
	env.resetArticles(t)
	env.resetAccounts(t)
	seedAIUpgradeTasks(t, env)
	ctx := context.Background()
	workflow, err := einoAdapter.NewWorkflow(ctx, e2eChatModel{})
	if err != nil {
		t.Fatal(err)
	}
	gen := &enrichment.ActiveProfile{Provider: "deterministic", Model: "chat-stub", ProfileVersion: "g-e2e", WorkflowVersion: "w1", PromptVersion: "p1", MaxAttempts: 2, AuditTokenBudget: 1000, StageBudget: time.Second}
	embed := &enrichment.ActiveProfile{Provider: "deterministic", Model: "embed-stub", ProfileVersion: "e-e2e", InputVersion: "i1", Dimensions: 3, MaxAttempts: 2, AuditTokenBudget: 1000, StageBudget: time.Second}
	executor := enrichment.NewExecutor(postgres.NewEnrichmentRepository(env.pool), workflow, einoAdapter.NewEmbeddingAdapter(e2eEmbedder{}, 3), gen, embed, enrichment.ExecutorPolicy{Lease: time.Minute, GenerationBackoffMin: time.Second, GenerationBackoffMax: time.Minute, EmbeddingBackoffMin: time.Second, EmbeddingBackoffMax: time.Minute, ChunkChars: 1000, MaxChunks: 8, SingleInputChars: 12000, MaxCalls: 9, Concurrency: 2, MaxOutputTokens: 1000, OutputLimits: enrichment.OutputLimits{SummaryChars: 1000, KeywordCount: 12, TopicCount: 5, LabelChars: 64}, EmbeddingBodyChars: 12000}, nil, nil)
	for range 2 {
		processed, runErr := executor.ProcessOne(ctx, "e2e-worker")
		if runErr != nil || !processed {
			t.Fatalf("processed=%t err=%v", processed, runErr)
		}
	}
	detail, err := postgres.NewArticleRepository(env.pool).GetPublished(ctx, 7101)
	if err != nil || detail.Item.Enhancement == nil || detail.Item.Enhancement.Summary != "E2E AI 摘要" {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	var dimensions int
	var vector []float32
	if err := env.pool.QueryRow(ctx, `SELECT dimensions,vector FROM velis.ai_embedding_results WHERE article_id=7101`).Scan(&dimensions, &vector); err != nil || dimensions != 3 || len(vector) != 3 {
		t.Fatalf("vector dimensions=%d len=%d err=%v", dimensions, len(vector), err)
	}
	// 未配置模型不会阻止另一篇公开文章读取，且增强显式为空。
	plain, err := postgres.NewArticleRepository(env.pool).GetPublished(ctx, 7102)
	if err != nil || plain.Item.Enhancement != nil {
		t.Fatalf("无增强降级失败: %+v err=%v", plain.Item, err)
	}
}
