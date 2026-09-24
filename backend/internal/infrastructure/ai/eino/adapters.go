package eino

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	embeddingopenai "github.com/cloudwego/eino-ext/components/embedding/openai"
	modelopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/eino-contrib/jsonschema"
	sdkopenai "github.com/meguminnnnnnnnn/go-openai"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
)

func NewOpenAIWorkflow(ctx context.Context, cfg config.GenerationConfig) (*Workflow, error) {
	maxTokens := cfg.MaxOutputTokens
	maxTokensField, maxCompletionTokensField := outputTokenFields(cfg.MaxTokensParam, maxTokens)
	mapChat, err := modelopenai.NewChatModel(ctx, &modelopenai.ChatModelConfig{
		APIKey: cfg.Profile.APIKey, BaseURL: cfg.Profile.BaseURL, Model: cfg.Profile.Model,
		Timeout: cfg.Profile.Timeout, MaxTokens: maxTokensField, MaxCompletionTokens: maxCompletionTokensField,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 generation Map ChatModel: %w", err)
	}
	responseFormat, err := generationResponseFormat(cfg)
	if err != nil {
		return nil, err
	}
	finalChat, err := modelopenai.NewChatModel(ctx, &modelopenai.ChatModelConfig{
		APIKey: cfg.Profile.APIKey, BaseURL: cfg.Profile.BaseURL, Model: cfg.Profile.Model,
		Timeout: cfg.Profile.Timeout, MaxTokens: maxTokensField, MaxCompletionTokens: maxCompletionTokensField, ResponseFormat: responseFormat,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 generation Final ChatModel: %w", err)
	}
	return NewWorkflow(ctx, mapChat, finalChat)
}

// outputTokenFields 只发送部署选定的输出上限参数名：不同 OpenAI-compatible Provider 对 max_tokens 与
// max_completion_tokens 的支持不一致，发错参数名时上限会静默失效，因此这里按配置显式二选一。
func outputTokenFields(parameter string, maxTokens int) (*int, *int) {
	if parameter == "max_completion_tokens" {
		return nil, &maxTokens
	}
	return &maxTokens, nil
}

func generationResponseFormat(cfg config.GenerationConfig) (*modelopenai.ChatCompletionResponseFormat, error) {
	switch cfg.StructuredOutput {
	case "prompt":
		return nil, nil
	case "json_object":
		return &modelopenai.ChatCompletionResponseFormat{Type: modelopenai.ChatCompletionResponseFormatTypeJSONObject}, nil
	case "json_schema":
		schemaBytes, err := json.Marshal(enrichment.GeneratedContentJSONSchema(enrichment.OutputLimits{
			SummaryChars: cfg.SummaryMaxChars, KeywordCount: cfg.KeywordMaxCount, TopicCount: cfg.TopicMaxCount, LabelChars: cfg.LabelMaxChars,
		}))
		if err != nil {
			return nil, fmt.Errorf("编码 generation JSON Schema: %w", err)
		}
		var outputSchema jsonschema.Schema
		if err := json.Unmarshal(schemaBytes, &outputSchema); err != nil {
			return nil, fmt.Errorf("构造 generation JSON Schema: %w", err)
		}
		return &modelopenai.ChatCompletionResponseFormat{
			Type: modelopenai.ChatCompletionResponseFormatTypeJSONSchema,
			JSONSchema: &modelopenai.ChatCompletionResponseFormatJSONSchema{
				Name: "velis_content_enrichment", Description: "文章摘要、关键词和主题", Strict: true, JSONSchema: &outputSchema,
			},
		}, nil
	default:
		return nil, fmt.Errorf("不支持的结构化输出模式 %q", cfg.StructuredOutput)
	}
}

type EmbeddingAdapter struct {
	embedder   embedding.Embedder
	dimensions int
}

func NewOpenAIEmbedder(ctx context.Context, cfg config.EmbeddingConfig) (*EmbeddingAdapter, error) {
	dimensions := cfg.Dimensions
	var requestDimensions *int
	if cfg.RequestDimensions {
		requestDimensions = &dimensions
	}
	embedder, err := embeddingopenai.NewEmbedder(ctx, &embeddingopenai.EmbeddingConfig{
		APIKey: cfg.Profile.APIKey, BaseURL: cfg.Profile.BaseURL, Model: cfg.Profile.Model,
		Timeout: cfg.Profile.Timeout, Dimensions: requestDimensions,
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
		err := enrichment.NewErrorWithReason(enrichment.ErrorInvalidOutput, true, enrichment.ReasonVectorDimensions, "Embedding 返回数量不符", nil)
		record.Status, record.ErrorCode, record.ErrorBrief, record.ErrorReason = "failed", enrichment.ErrorInvalidOutput, "Embedding 输出未通过严格校验", enrichment.ErrorReason(err)
		return enrichment.EmbeddingResponse{Call: record}, err
	}
	if err := enrichment.ValidateVector(vectors[0], a.dimensions); err != nil {
		record.Status, record.ErrorCode, record.ErrorBrief, record.ErrorReason = "failed", enrichment.ErrorInvalidOutput, "Embedding 输出未通过严格校验", enrichment.ErrorReason(err)
		return enrichment.EmbeddingResponse{Call: record}, err
	}
	return enrichment.EmbeddingResponse{Vector: vectors[0], Call: record}, nil
}

func classifyModelError(err error) (enrichment.ErrorCode, bool) {
	if errors.Is(err, context.DeadlineExceeded) {
		return enrichment.ErrorTimeout, true
	}
	var apiErr *modelopenai.APIError
	if errors.As(err, &apiErr) {
		return classifyHTTPStatus(apiErr.HTTPStatusCode)
	}
	var sdkAPIErr *sdkopenai.APIError
	if errors.As(err, &sdkAPIErr) {
		return classifyHTTPStatus(sdkAPIErr.HTTPStatusCode)
	}
	var requestErr *sdkopenai.RequestError
	if errors.As(err, &requestErr) && requestErr.HTTPStatusCode > 0 {
		return classifyHTTPStatus(requestErr.HTTPStatusCode)
	}
	// 上层取消不是 Provider 故障，必须与 internal 区分；同时要先于 net.Error 判定，否则会被 url.Error 包装误判为 network。
	if errors.Is(err, context.Canceled) {
		return enrichment.ErrorCanceled, true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return enrichment.ErrorNetwork, true
	}
	return enrichment.ErrorInternal, false
}

func classifyHTTPStatus(status int) (enrichment.ErrorCode, bool) {
	switch status {
	case 401, 403:
		return enrichment.ErrorAuthentication, false
	case 408:
		return enrichment.ErrorTimeout, true
	case 429:
		return enrichment.ErrorRateLimited, true
	default:
		if status >= 500 {
			return enrichment.ErrorProviderUnavailable, true
		}
		if status >= 400 {
			return enrichment.ErrorInputUnsupported, false
		}
	}
	return enrichment.ErrorInternal, false
}

func safeErrorBrief(err error) string {
	code, _ := classifyModelError(err)
	switch code {
	case enrichment.ErrorTimeout:
		return "模型调用超时"
	case enrichment.ErrorCanceled:
		return "模型调用已取消"
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
