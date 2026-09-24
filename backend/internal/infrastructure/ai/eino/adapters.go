package eino

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	embeddingopenai "github.com/cloudwego/eino-ext/components/embedding/openai"
	modelopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/embedding"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
)

func NewOpenAIWorkflow(ctx context.Context, cfg config.GenerationConfig) (*Workflow, error) {
	maxTokens := cfg.MaxOutputTokens
	chat, err := modelopenai.NewChatModel(ctx, &modelopenai.ChatModelConfig{
		APIKey: cfg.Profile.APIKey, BaseURL: cfg.Profile.BaseURL, Model: cfg.Profile.Model,
		Timeout: cfg.Profile.Timeout, MaxCompletionTokens: &maxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 generation ChatModel: %w", err)
	}
	return NewWorkflow(ctx, chat)
}

type EmbeddingAdapter struct {
	embedder   embedding.Embedder
	dimensions int
}

func NewOpenAIEmbedder(ctx context.Context, cfg config.EmbeddingConfig) (*EmbeddingAdapter, error) {
	dimensions := cfg.Dimensions
	embedder, err := embeddingopenai.NewEmbedder(ctx, &embeddingopenai.EmbeddingConfig{
		APIKey: cfg.Profile.APIKey, BaseURL: cfg.Profile.BaseURL, Model: cfg.Profile.Model,
		Timeout: cfg.Profile.Timeout, Dimensions: &dimensions,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 Embedding 组件: %w", err)
	}
	return NewEmbeddingAdapter(embedder, dimensions), nil
}

func NewEmbeddingAdapter(embedder embedding.Embedder, dimensions int) *EmbeddingAdapter {
	return &EmbeddingAdapter{embedder: embedder, dimensions: dimensions}
}

func (a *EmbeddingAdapter) Embed(ctx context.Context, request enrichment.EmbeddingRequest) (enrichment.EmbeddingResponse, error) {
	started := time.Now()
	vectors, err := a.embedder.EmbedStrings(ctx, []string{request.Document})
	record := enrichment.CallRecord{Kind: "embedding", InputHash: request.InputHash, Duration: time.Since(started), Status: "succeeded"}
	if err != nil {
		code, retryable := classifyModelError(err)
		record.Status, record.ErrorCode, record.ErrorBrief = "failed", code, safeErrorBrief(err)
		return enrichment.EmbeddingResponse{Call: record}, enrichment.NewError(code, retryable, "Embedding 调用失败", err)
	}
	if len(vectors) != 1 {
		record.Status, record.ErrorCode = "failed", enrichment.ErrorInvalidOutput
		return enrichment.EmbeddingResponse{Call: record}, enrichment.NewError(enrichment.ErrorInvalidOutput, true, "Embedding 返回数量不符", nil)
	}
	if err := enrichment.ValidateVector(vectors[0], a.dimensions); err != nil {
		record.Status, record.ErrorCode = "failed", enrichment.ErrorInvalidOutput
		return enrichment.EmbeddingResponse{Call: record}, err
	}
	return enrichment.EmbeddingResponse{Vector: vectors[0], Call: record}, nil
}

func classifyModelError(err error) (enrichment.ErrorCode, bool) {
	if errors.Is(err, context.DeadlineExceeded) {
		return enrichment.ErrorTimeout, true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return enrichment.ErrorNetwork, true
	}
	var apiErr *modelopenai.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.HTTPStatusCode {
		case 401, 403:
			return enrichment.ErrorAuthentication, false
		case 408:
			return enrichment.ErrorTimeout, true
		case 429:
			return enrichment.ErrorRateLimited, true
		default:
			if apiErr.HTTPStatusCode >= 500 {
				return enrichment.ErrorProviderUnavailable, true
			}
			if apiErr.HTTPStatusCode >= 400 {
				return enrichment.ErrorInputUnsupported, false
			}
		}
	}
	return enrichment.ErrorInternal, false
}

func safeErrorBrief(err error) string {
	code, _ := classifyModelError(err)
	switch code {
	case enrichment.ErrorTimeout:
		return "模型调用超时"
	case enrichment.ErrorRateLimited:
		return "模型服务限流"
	case enrichment.ErrorProviderUnavailable:
		return "模型服务暂时不可用"
	case enrichment.ErrorNetwork:
		return "模型网络调用失败"
	case enrichment.ErrorAuthentication:
		return "模型鉴权失败"
	case enrichment.ErrorInputUnsupported:
		return "模型拒绝请求"
	default:
		return "模型调用内部错误"
	}
}
