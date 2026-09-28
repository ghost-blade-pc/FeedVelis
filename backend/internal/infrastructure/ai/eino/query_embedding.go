package eino

import (
	"context"
	embeddingopenai "github.com/cloudwego/eino-ext/components/embedding/openai"
	"net/http"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
)

// QueryEmbeddingAdapter 与离线任务的预算、审计和重试完全独立。
type QueryEmbeddingAdapter struct {
	embedder   embedding.Embedder
	dimensions int
	timeout    time.Duration
	transport  *http.Transport
}

func NewQueryEmbeddingAdapter(embedder embedding.Embedder, dimensions int, timeout time.Duration) *QueryEmbeddingAdapter {
	return &QueryEmbeddingAdapter{embedder: embedder, dimensions: dimensions, timeout: timeout}
}

func NewOpenAIQueryEmbedder(ctx context.Context, cfg config.EmbeddingConfig, timeout time.Duration) (*QueryEmbeddingAdapter, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 8
	client := &http.Client{Transport: transport, Timeout: timeout}
	var dimensions *int
	if cfg.RequestDimensions {
		value := cfg.Dimensions
		dimensions = &value
	}
	embedder, err := embeddingopenai.NewEmbedder(ctx, &embeddingopenai.EmbeddingConfig{APIKey: cfg.Profile.APIKey, BaseURL: cfg.Profile.BaseURL, Model: cfg.Profile.Model, HTTPClient: client, Dimensions: dimensions})
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	adapter := NewQueryEmbeddingAdapter(embedder, cfg.Dimensions, timeout)
	adapter.transport = transport
	return adapter, nil
}

func (a *QueryEmbeddingAdapter) EmbedQuery(ctx context.Context, query string) ([]float64, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	vectors, err := a.embedder.EmbedStrings(ctx, []string{query})
	if err != nil {
		code, retryable := classifyModelError(err)
		return nil, &articlesearch.QueryEmbeddingFailure{Class: string(code), Cause: enrichment.NewError(code, retryable, "查询 Embedding 调用失败", err)}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if len(vectors) != 1 || !articlesearch.ValidQueryVector(vectors[0], a.dimensions) {
		return nil, &articlesearch.QueryEmbeddingFailure{Class: string(enrichment.ErrorInvalidOutput), Cause: enrichment.NewError(enrichment.ErrorInvalidOutput, false, "查询向量无效", nil)}
	}
	return vectors[0], nil
}

func (a *QueryEmbeddingAdapter) Close() error {
	if a.transport != nil {
		a.transport.CloseIdleConnections()
	}
	return nil
}
