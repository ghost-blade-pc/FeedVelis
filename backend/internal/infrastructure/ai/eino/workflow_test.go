package eino

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
)

type deterministicChat struct {
	mu                       sync.Mutex
	calls, active, maxActive int
	delay                    time.Duration
	invalid                  bool
}

func (m *deterministicChat) Generate(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	m.calls++
	m.active++
	if m.active > m.maxActive {
		m.maxActive = m.active
	}
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.active--; m.mu.Unlock() }()
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	prompt := input[len(input)-1].Content
	message := schema.AssistantMessage("分块摘要", nil)
	if strings.Contains(prompt, "MAP_CHUNK") {
		message.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}
		return message, nil
	}
	content := `{"summary":"确定性摘要","keywords":["关键词"],"topics":["主题"]}`
	if m.invalid {
		content = "```json\n{}\n```"
	}
	message = schema.AssistantMessage(content, nil)
	message.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}
	return message, nil
}
func (m *deterministicChat) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("测试不使用流式接口")
}

func request(chunks []string) enrichment.GenerationRequest {
	return enrichment.GenerationRequest{Revision: enrichment.RevisionInput{Title: "标题", Language: "zh-CN"}, InputHash: strings.Repeat("a", 64), Chunks: chunks,
		PromptVersion: "p-v1", WorkflowVersion: "w-v1", MaxOutputTokens: 1200, AuditTokenBudget: 1000, MaxCalls: 9, Concurrency: 2, TotalTimeout: time.Second,
		Limits: enrichment.OutputLimits{SummaryChars: 1000, KeywordCount: 12, TopicCount: 5, LabelChars: 64}}
}

func TestWorkflowShortAndLongPaths(t *testing.T) {
	chat := &deterministicChat{delay: 5 * time.Millisecond}
	workflow, err := NewWorkflow(context.Background(), chat)
	if err != nil {
		t.Fatal(err)
	}
	short, err := workflow.Generate(context.Background(), request([]string{"短文"}))
	if err != nil || short.Content.Summary != "确定性摘要" || len(short.Calls) != 1 || short.Calls[0].Usage.TotalTokens == nil {
		t.Fatalf("短文结果=%+v err=%v", short, err)
	}
	longRequest := request([]string{"一", "二", "三", "四"})
	long, err := workflow.Generate(context.Background(), longRequest)
	if err != nil || len(long.Calls) != 5 || chat.maxActive > 2 {
		t.Fatalf("长文 calls=%d maxActive=%d err=%v", len(long.Calls), chat.maxActive, err)
	}
}

func TestWorkflowBudgetInvalidOutputAndCancellation(t *testing.T) {
	chat := &deterministicChat{}
	workflow, _ := NewWorkflow(context.Background(), chat)
	budget := request([]string{"一", "二"})
	budget.MaxCalls = 2
	if _, err := workflow.Generate(context.Background(), budget); err == nil {
		t.Fatal("调用预算不足应中止")
	}
	tokenChat := &deterministicChat{}
	workflow, _ = NewWorkflow(context.Background(), tokenChat)
	tokenBudget := request([]string{"一", "二"})
	tokenBudget.AuditTokenBudget = 20
	response, err := workflow.Generate(context.Background(), tokenBudget)
	code, _ := enrichment.ErrorClassification(err)
	if code != enrichment.ErrorBudgetExceeded || len(response.Calls) != 2 || tokenChat.calls != 2 {
		t.Fatalf("实际 Token 超预算仍执行 reduce: code=%s records=%d calls=%d", code, len(response.Calls), tokenChat.calls)
	}
	chat.invalid = true
	workflow, _ = NewWorkflow(context.Background(), chat)
	if _, err := workflow.Generate(context.Background(), request([]string{"短文"})); err == nil {
		t.Fatal("非法结构应拒绝")
	}
	slow := &deterministicChat{delay: time.Second}
	workflow, _ = NewWorkflow(context.Background(), slow)
	timed := request([]string{"短文"})
	timed.TotalTimeout = 10 * time.Millisecond
	if _, err := workflow.Generate(context.Background(), timed); err == nil {
		t.Fatal("总超时应取消")
	}
}

type deterministicEmbedder struct {
	vectors [][]float64
	err     error
}

func (e deterministicEmbedder) EmbedStrings(context.Context, []string, ...embedding.Option) ([][]float64, error) {
	return e.vectors, e.err
}

func TestEmbeddingAdapterValidatesOutput(t *testing.T) {
	request := enrichment.EmbeddingRequest{Document: "检索文档", InputHash: strings.Repeat("b", 64), InputVersion: "v1"}
	valid, err := NewEmbeddingAdapter(deterministicEmbedder{vectors: [][]float64{{1, 2}}}, 2).Embed(context.Background(), request)
	if err != nil || len(valid.Vector) != 2 || valid.Call.InputHash != request.InputHash {
		t.Fatalf("valid=%+v err=%v", valid, err)
	}
	for _, vectors := range [][][]float64{nil, {}, {{1}}, {{1, 2}, {3, 4}}} {
		if _, err := NewEmbeddingAdapter(deterministicEmbedder{vectors: vectors}, 2).Embed(context.Background(), request); err == nil {
			t.Fatalf("应拒绝: %v", vectors)
		}
	}
}
