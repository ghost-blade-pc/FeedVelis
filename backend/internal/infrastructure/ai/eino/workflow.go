// Package eino 实现经过 CloudWeGo Eino 编译的有界内容增强 Workflow。
package eino

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"golang.org/x/sync/errgroup"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
)

type Workflow struct {
	runner compose.Runnable[enrichment.GenerationRequest, workflowResult]
}

type workflowResult struct {
	Response enrichment.GenerationResponse
	Err      error
}

func NewWorkflow(ctx context.Context, chat model.BaseChatModel) (*Workflow, error) {
	if chat == nil {
		return nil, fmt.Errorf("ChatModel 不能为空")
	}
	graph := compose.NewGraph[enrichment.GenerationRequest, workflowResult]()
	runner := &workflowRunner{chat: chat}
	if err := graph.AddLambdaNode("bounded_generation", compose.InvokableLambda(func(ctx context.Context, request enrichment.GenerationRequest) (workflowResult, error) {
		response, err := runner.run(ctx, request)
		return workflowResult{Response: response, Err: err}, nil
	})); err != nil {
		return nil, err
	}
	if err := graph.AddEdge(compose.START, "bounded_generation"); err != nil {
		return nil, err
	}
	if err := graph.AddEdge("bounded_generation", compose.END); err != nil {
		return nil, err
	}
	compiled, err := graph.Compile(ctx, compose.WithGraphName("velis-ai-content-enrichment"))
	if err != nil {
		return nil, fmt.Errorf("编译 Eino 内容增强 Workflow: %w", err)
	}
	return &Workflow{runner: compiled}, nil
}

func (w *Workflow) Generate(ctx context.Context, request enrichment.GenerationRequest) (enrichment.GenerationResponse, error) {
	result, err := w.runner.Invoke(ctx, request)
	if err != nil {
		return enrichment.GenerationResponse{}, err
	}
	return result.Response, result.Err
}

type workflowRunner struct{ chat model.BaseChatModel }

func (w *workflowRunner) run(ctx context.Context, request enrichment.GenerationRequest) (enrichment.GenerationResponse, error) {
	if len(request.Chunks) == 0 || (len(request.Chunks) == 1 && strings.TrimSpace(request.Chunks[0]) == "") {
		return enrichment.GenerationResponse{}, enrichment.NewError(enrichment.ErrorInputUnsupported, false, "文章正文为空", nil)
	}
	requiredCalls := 1
	long := len(request.Chunks) > 1
	if long {
		requiredCalls = len(request.Chunks) + 1
	}
	if requiredCalls > request.MaxCalls {
		return enrichment.GenerationResponse{}, enrichment.NewError(enrichment.ErrorBudgetExceeded, false, "生成调用数预算不足", nil)
	}
	if request.Concurrency < 1 {
		return enrichment.GenerationResponse{}, enrichment.NewError(enrichment.ErrorConfiguration, false, "生成并发配置无效", nil)
	}
	if request.TotalTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, request.TotalTimeout)
		defer cancel()
	}
	if !long {
		raw, call, err := w.call(ctx, "generation_single", finalPrompt(request, request.Chunks[0]))
		if err != nil {
			return enrichment.GenerationResponse{Calls: []enrichment.CallRecord{call}}, err
		}
		content, err := enrichment.ParseGeneratedContent([]byte(raw), request.Limits)
		if err != nil {
			call.Status = "failed"
			call.ErrorCode = enrichment.ErrorInvalidOutput
			return enrichment.GenerationResponse{Calls: []enrichment.CallRecord{call}}, err
		}
		return enrichment.GenerationResponse{Content: content, Calls: []enrichment.CallRecord{call}}, nil
	}

	summaries := make([]string, len(request.Chunks))
	calls := make([]enrichment.CallRecord, len(request.Chunks), len(request.Chunks)+1)
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(request.Concurrency)
	for index, chunk := range request.Chunks {
		index, chunk := index, chunk
		group.Go(func() error {
			raw, call, err := w.call(groupCtx, "generation_map", mapPrompt(request, index, chunk))
			calls[index] = call
			if err != nil {
				return err
			}
			summary := strings.TrimSpace(raw)
			if summary == "" {
				calls[index].Status = "failed"
				calls[index].ErrorCode = enrichment.ErrorInvalidOutput
				return enrichment.NewError(enrichment.ErrorInvalidOutput, true, "分块摘要为空", nil)
			}
			summaries[index] = summary
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return enrichment.GenerationResponse{Calls: calls}, err
	}
	if usageExceeds(calls, request.AuditTokenBudget) {
		return enrichment.GenerationResponse{Calls: calls}, enrichment.NewError(enrichment.ErrorBudgetExceeded, false, "generation Token 审计预算已超出", nil)
	}
	raw, finalCall, err := w.call(ctx, "generation_reduce", finalPrompt(request, strings.Join(summaries, "\n\n")))
	calls = append(calls, finalCall)
	if err != nil {
		return enrichment.GenerationResponse{Calls: calls}, err
	}
	content, err := enrichment.ParseGeneratedContent([]byte(raw), request.Limits)
	if err != nil {
		calls[len(calls)-1].Status = "failed"
		calls[len(calls)-1].ErrorCode = enrichment.ErrorInvalidOutput
		return enrichment.GenerationResponse{Calls: calls}, err
	}
	return enrichment.GenerationResponse{Content: content, Calls: calls}, nil
}

func usageExceeds(calls []enrichment.CallRecord, budget int) bool {
	if budget <= 0 {
		return false
	}
	total := 0
	for _, call := range calls {
		if call.Usage.TotalTokens != nil {
			total += *call.Usage.TotalTokens
		}
	}
	return total > budget
}

func (w *workflowRunner) call(ctx context.Context, kind, prompt string) (string, enrichment.CallRecord, error) {
	started := time.Now()
	message, err := w.chat.Generate(ctx, []*schema.Message{schema.SystemMessage("只按要求返回内容，不要使用 Markdown 围栏。"), schema.UserMessage(prompt)})
	hash := sha256.Sum256([]byte(prompt))
	record := enrichment.CallRecord{Kind: kind, InputHash: hex.EncodeToString(hash[:]), Duration: time.Since(started), Status: "succeeded"}
	if err != nil {
		code, retryable := classifyModelError(err)
		record.Status, record.ErrorCode, record.ErrorBrief = "failed", code, safeErrorBrief(err)
		return "", record, enrichment.NewError(code, retryable, "模型调用失败", err)
	}
	if message.ResponseMeta != nil && message.ResponseMeta.Usage != nil &&
		(message.ResponseMeta.Usage.PromptTokens > 0 || message.ResponseMeta.Usage.CompletionTokens > 0 || message.ResponseMeta.Usage.TotalTokens > 0) {
		u := message.ResponseMeta.Usage
		in, out, total := u.PromptTokens, u.CompletionTokens, u.TotalTokens
		record.Usage = enrichment.Usage{InputTokens: &in, OutputTokens: &out, TotalTokens: &total}
	}
	return message.Content, record, nil
}

func mapPrompt(request enrichment.GenerationRequest, index int, chunk string) string {
	return fmt.Sprintf("任务: MAP_CHUNK\nworkflow_version: %s\nprompt_version: %s\nchunk_index: %d\n请将以下正文分块压缩为纯文本摘要：\n%s", request.WorkflowVersion, request.PromptVersion, index, chunk)
}

func finalPrompt(request enrichment.GenerationRequest, text string) string {
	return fmt.Sprintf("任务: FINAL_JSON\nworkflow_version: %s\nprompt_version: %s\nlanguage: %s\ntitle: %s\n只返回包含 summary、keywords、topics 的 JSON 对象。\n输入：\n%s", request.WorkflowVersion, request.PromptVersion, request.Revision.Language, request.Revision.Title, text)
}
