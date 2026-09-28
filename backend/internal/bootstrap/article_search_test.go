package bootstrap

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
)

func TestBuildArticleSearchDegradesWithoutConfigurationOrProductionKey(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default()
	service, closer := buildArticleSearch(cfg, nil, searchApp.NopObserver{}, logger)
	if closer != nil {
		t.Fatal("未配置 OpenSearch 不应创建客户端")
	}
	if _, err := service.Search(context.Background(), searchApp.Request{Q: "go"}); searchApp.CodeOf(err) != searchApp.CodeSearchUnavailable {
		t.Fatalf("未配置搜索应稳定返回 unavailable: %v", err)
	}

	production := config.Default()
	production.App.Environment = "production"
	production.Search.Endpoints = []string{"https://search.internal:9200"}
	production.Search.Username, production.Search.Password = "reader", "secret"
	if err := production.Validate(); err != nil {
		t.Fatal(err)
	}
	service, closer = buildArticleSearch(production, nil, searchApp.NopObserver{}, logger)
	if closer != nil || production.Search.QueryEnabled() {
		t.Fatal("生产缺 cursor key 时只能装配 unavailable service")
	}
}

func TestBuildArticleSearchCreatesClientWithoutStartupPing(t *testing.T) {
	cfg := config.Default()
	cfg.Search.Endpoints = []string{"http://127.0.0.1:1"} // 不存在的端点；构造阶段不得联网。
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	service, closer := buildArticleSearch(cfg, nil, searchApp.NopObserver{}, nil)
	if closer == nil {
		t.Fatal("配置完整时应构造查询客户端")
	}
	defer closer.Close()
	if _, ok := service.(*searchApp.Service); !ok {
		t.Fatalf("未联网时不应降级: %T", service)
	}
}

type blockingQueryEmbedder struct {
	started  chan struct{}
	finished chan struct{}
}

func (e blockingQueryEmbedder) EmbedQuery(ctx context.Context, _ string) ([]float64, error) {
	close(e.started)
	<-ctx.Done()
	close(e.finished)
	return nil, ctx.Err()
}
func TestQueryEmbeddingLifecycleCancellation(t *testing.T) {
	lifetime, cancel := context.WithCancel(context.Background())
	fake := blockingQueryEmbedder{make(chan struct{}), make(chan struct{})}
	adapter := cancellableQueryEmbedder(fake, lifetime)
	done := make(chan error, 1)
	go func() { _, err := adapter.EmbedQuery(context.Background(), "查询"); done <- err }()
	<-fake.started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("API 停止未取消模型请求")
	}
}

func TestBuildArticleSearchAcceptsEmbeddingOnlyWithoutOfflineWorkflow(t *testing.T) {
	cfg := config.Default()
	cfg.Search.Endpoints = []string{"http://127.0.0.1:1"}
	cfg.Search.Query.Hybrid.Enabled = true
	cfg.AI.Embedding.Profile.Provider = "openai-compatible"
	cfg.AI.Embedding.Profile.BaseURL = "http://127.0.0.1:1"
	cfg.AI.Embedding.Profile.APIKey = "test"
	cfg.AI.Embedding.Profile.Model = "test"
	cfg.AI.Embedding.Dimensions = 1024
	if cfg.AI.Generation.Profile.Enabled() {
		t.Fatal("夹具不应启用离线生成")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	service, closer := buildArticleSearch(cfg, nil, searchApp.NopObserver{}, nil)
	if closer == nil {
		t.Fatal("仅 Embedding 配置未装配")
	}
	defer closer.Close()
	if _, ok := service.(*searchApp.Service); !ok {
		t.Fatalf("装配失败: %T", service)
	}
}

type countCloser struct{ calls int }

func (c *countCloser) Close() error { c.calls++; return nil }
func TestSearchCloserReleasesBothResources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client, model := &countCloser{}, &countCloser{}
	closer := &searchCloser{client: client, model: model, cancel: cancel}
	if err := closer.Close(); err != nil || ctx.Err() != context.Canceled || client.calls != 1 || model.calls != 1 {
		t.Fatal("模型或搜索资源未回收")
	}
}
