package eino

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/enrichment"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
)

func TestOnlineQueryEmbeddingSingleCall(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		vector []float64
		slow   bool
		code   enrichment.ErrorCode
	}{
		{"成功", 200, []float64{1, 0}, false, ""}, {"零范数", 200, []float64{0, 0}, false, enrichment.ErrorInvalidOutput},
		{"维度", 200, []float64{1}, false, enrichment.ErrorInvalidOutput}, {"限流", 429, nil, false, enrichment.ErrorRateLimited},
		{"超时", 200, []float64{1, 0}, true, enrichment.ErrorTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body struct {
					Input []string `json:"input"`
					Model string   `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body.Input) != 1 || body.Input[0] != "规范查询 Go 中文" || body.Model != "query-model" || r.URL.Path != "/embeddings" {
					t.Errorf("查询输入不符: %+v", body)
				}
				if tc.slow {
					select {
					case <-r.Context().Done():
						return
					case <-time.After(200 * time.Millisecond):
					}
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				if tc.status != 200 {
					_, _ = w.Write([]byte(`{"error":{"message":"private input","type":"rate_limit","code":"rate_limit"}}`))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": tc.vector}}})
			}))
			defer server.Close()
			cfg := config.Default().AI.Embedding
			cfg.Dimensions = 2
			cfg.Profile = config.ModelProfileConfig{BaseURL: server.URL, APIKey: "key", Model: "query-model"}
			adapter, err := NewOpenAIQueryEmbedder(context.Background(), cfg, 50*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.EmbedQuery(context.Background(), "规范查询 Go 中文")
			if tc.code == "" && err != nil {
				t.Fatal(err)
			}
			code, _ := enrichment.ErrorClassification(err)
			if tc.code != "" && code != tc.code {
				t.Fatalf("分类 %v: %v", tc.code, err)
			}
			if calls.Load() != 1 {
				t.Fatalf("在线重试: %d", calls.Load())
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = adapter.EmbedQuery(canceled, "规范查询 Go 中文")
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("取消未传播: %v", err)
			}
		})
	}
}

func TestQueryEmbeddingRejectsNonfiniteAndEmpty(t *testing.T) {
	for _, vectors := range [][][]float64{nil, {{}}, {{0, 0}}, {{math.NaN(), 1}}, {{math.Inf(1), 1}}, {{1, 0}, {1, 0}}} {
		_, err := NewQueryEmbeddingAdapter(deterministicEmbedder{vectors: vectors}, 2, time.Second).EmbedQuery(context.Background(), "查询")
		if err == nil {
			t.Fatalf("接受无效向量: %v", vectors)
		}
	}
}
