package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"testing"

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
