package eino

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
)

func TestIndependentOpenAICompatibleProfiles(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	chatServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen["chat_path"], seen["chat_auth"] = r.URL.Path, r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chat-1","object":"chat.completion","created":1,"model":"chat-test","choices":[{"index":0,"message":{"role":"assistant","content":"{\"summary\":\"摘要\",\"keywords\":[\"词\"],\"topics\":[\"主题\"]}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":6,"total_tokens":15}}`))
	}))
	defer chatServer.Close()
	embedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen["embed_path"], seen["embed_auth"] = r.URL.Path, r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "model": "embed-test", "data": []any{map[string]any{"object": "embedding", "index": 0, "embedding": []float64{0.25, 0.75}}}})
	}))
	defer embedServer.Close()

	genCfg := config.Default().AI.Generation
	genCfg.Profile = config.ModelProfileConfig{Provider: "chat-provider", BaseURL: chatServer.URL, APIKey: "chat-secret", Model: "chat-test", ProfileVersion: "g-v1", Timeout: time.Second}
	workflow, err := NewOpenAIWorkflow(context.Background(), genCfg)
	if err != nil {
		t.Fatal(err)
	}
	response, err := workflow.Generate(context.Background(), request([]string{"正文"}))
	if err != nil || response.Content.Summary != "摘要" || response.Calls[0].Usage.TotalTokens == nil || *response.Calls[0].Usage.TotalTokens != 15 {
		t.Fatalf("generation=%+v err=%v", response, err)
	}

	embedCfg := config.Default().AI.Embedding
	embedCfg.Profile = config.ModelProfileConfig{Provider: "embed-provider", BaseURL: embedServer.URL, APIKey: "embed-secret", Model: "embed-test", ProfileVersion: "e-v1", Timeout: time.Second}
	embedCfg.Dimensions = 2
	embedder, err := NewOpenAIEmbedder(context.Background(), embedCfg)
	if err != nil {
		t.Fatal(err)
	}
	vector, err := embedder.Embed(context.Background(), enrichment.EmbeddingRequest{Document: "文档", InputHash: strings.Repeat("a", 64), InputVersion: "i-v1"})
	if err != nil || len(vector.Vector) != 2 {
		t.Fatalf("embedding=%+v err=%v", vector, err)
	}
	if seen["chat_auth"] != "Bearer chat-secret" || seen["embed_auth"] != "Bearer embed-secret" || seen["chat_path"] == seen["embed_path"] {
		t.Fatalf("独立端点或密钥未生效: %+v", seen)
	}
}

func TestOpenAIErrorClassificationAndNullableUsage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		delay  time.Duration
		want   enrichment.ErrorCode
	}{
		{"鉴权", http.StatusUnauthorized, 0, enrichment.ErrorAuthentication},
		{"限流", http.StatusTooManyRequests, 0, enrichment.ErrorRateLimited},
		{"服务端", http.StatusServiceUnavailable, 0, enrichment.ErrorProviderUnavailable},
		{"超时", http.StatusOK, 50 * time.Millisecond, enrichment.ErrorTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.delay > 0 {
					time.Sleep(tc.delay)
				}
				if tc.status != http.StatusOK {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(`{"error":{"message":"secret-key at https://private.example/v1","type":"test"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"{}"},"finish_reason":"stop"}]}`))
			}))
			defer server.Close()
			cfg := config.Default().AI.Generation
			cfg.Profile = config.ModelProfileConfig{Provider: "test", BaseURL: server.URL, APIKey: "secret-key", Model: "m", ProfileVersion: "v1", Timeout: 10 * time.Millisecond}
			workflow, err := NewOpenAIWorkflow(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			result, err := workflow.Generate(context.Background(), request([]string{"正文"}))
			if err == nil {
				t.Fatal("预期调用失败")
			}
			code, _ := enrichment.ErrorClassification(err)
			if code != tc.want {
				t.Fatalf("code=%s want=%s err=%v", code, tc.want, err)
			}
			if len(result.Calls) != 1 || strings.Contains(result.Calls[0].ErrorBrief, "secret-key") || strings.Contains(result.Calls[0].ErrorBrief, "http") {
				t.Fatalf("错误摘要未脱敏: %+v", result.Calls)
			}
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"{\"summary\":\"摘要\",\"keywords\":[\"词\"],\"topics\":[\"主题\"]}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	cfg := config.Default().AI.Generation
	cfg.Profile = config.ModelProfileConfig{Provider: "test", BaseURL: server.URL, APIKey: "key", Model: "m", ProfileVersion: "v1", Timeout: time.Second}
	workflow, _ := NewOpenAIWorkflow(context.Background(), cfg)
	result, err := workflow.Generate(context.Background(), request([]string{"正文"}))
	if err != nil || result.Calls[0].Usage.TotalTokens != nil {
		t.Fatalf("nullable usage 未保留: %+v err=%v", result, err)
	}
}
