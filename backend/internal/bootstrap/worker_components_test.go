package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/observability"
	"github.com/prometheus/client_golang/prometheus"
)

func TestWorkerComponentsDisableOnlyMQWhenURLIsEmpty(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	components, err := buildWorkerComponents(cfg, nil, logger, "worker-test")
	if err != nil {
		t.Fatal(err)
	}
	names := componentNames(components)
	for _, required := range []string{"feed", "outbox-cleanup", "metrics"} {
		if !names[required] {
			t.Errorf("MQ 关闭时缺少 %s", required)
		}
	}
	for _, disabled := range []string{"relay", "consumer"} {
		if names[disabled] {
			t.Errorf("MQ 关闭时不应装配 %s", disabled)
		}
	}
	cfg = config.Default()
	cfg.RabbitMQ.URL = "amqp://guest:guest@localhost:5672/"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	components, err = buildWorkerComponents(cfg, nil, logger, "worker-test")
	if err != nil {
		t.Fatal(err)
	}
	names = componentNames(components)
	if !names["relay"] || !names["consumer"] {
		t.Fatalf("MQ 开启后组件缺失: %+v", names)
	}
	cfg = config.Default()
	cfg.AI.Generation.Profile.Provider = "test"
	cfg.AI.Generation.Profile.BaseURL = "http://model.internal/v1"
	cfg.AI.Generation.Profile.APIKey = "secret"
	cfg.AI.Generation.Profile.Model = "chat"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	components, err = buildWorkerComponents(cfg, nil, logger, "worker-test")
	if err != nil {
		t.Fatal(err)
	}
	if !componentNames(components)["ai_enrichment"] {
		t.Fatal("仅 generation profile 时应装配独立 AI 组件")
	}
}
func componentNames(components []WorkerComponent) map[string]bool {
	result := map[string]bool{}
	for _, component := range components {
		result[component.Name] = true
	}
	return result
}

// TestWorkerComponentsAssembleSearchProjectionOnlyWhenConfigured 覆盖 5.2 的禁用语义：
// 未配置 OpenSearch 时不装配投影组件，配置后才装配。
func TestWorkerComponentsAssembleSearchProjectionOnlyWhenConfigured(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	components, err := buildWorkerComponents(cfg, nil, logger, "worker-test")
	if err != nil {
		t.Fatal(err)
	}
	if componentNames(components)["search_projection"] {
		t.Fatal("未配置 OpenSearch 时不得装配投影组件")
	}
	// 未配置时文章与 AI 组件照常装配。
	for _, required := range []string{"feed", "outbox-cleanup", "metrics"} {
		if !componentNames(components)[required] {
			t.Fatalf("未配置 OpenSearch 时缺少 %s", required)
		}
	}

	cfg = config.Default()
	cfg.Search.Endpoints = []string{"http://127.0.0.1:1"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	components, err = buildWorkerComponents(cfg, nil, logger, "worker-test")
	if err != nil {
		t.Fatal(err)
	}
	if !componentNames(components)["search_projection"] {
		t.Fatal("配置 OpenSearch 后必须装配投影组件")
	}
}

// TestSearchProjectionFailureDoesNotStopOtherComponents 覆盖 5.2 的故障隔离：
// OpenSearch 不可用时投影组件反复重启，Feed、清理与指标组件保持运行。
func TestSearchProjectionFailureDoesNotStopOtherComponents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default()
	cfg.Search.Endpoints = []string{"http://127.0.0.1:1"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	var healthy atomic.Int32
	components := []WorkerComponent{
		{Name: "feed", Run: func(ctx context.Context) error { healthy.Add(1); <-ctx.Done(); return nil }},
		{Name: "search_projection", Run: func(ctx context.Context) error {
			return runSearchProjection(ctx, cfg, nil, logger, "worker-test", observability.NewSearchMetrics(prometheus.NewRegistry()))
		}},
	}
	if err := NewSupervisor(components, logger, time.Millisecond, time.Second).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if healthy.Load() != 1 {
		t.Fatalf("搜索投影故障不得重启其他组件: healthy=%d", healthy.Load())
	}
}
