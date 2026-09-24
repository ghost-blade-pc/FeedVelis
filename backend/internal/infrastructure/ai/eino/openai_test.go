package eino

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	modelopenai "github.com/cloudwego/eino-ext/components/model/openai"
	sdkopenai "github.com/meguminnnnnnnnn/go-openai"

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

func TestOutputTokenCapParameterFollowsConfig(t *testing.T) {
	for _, tc := range []struct{ parameter, want, absent string }{
		{"max_tokens", "max_tokens", "max_completion_tokens"},
		{"max_completion_tokens", "max_completion_tokens", "max_tokens"},
	} {
		t.Run(tc.parameter, func(t *testing.T) {
			var mu sync.Mutex
			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var decoded map[string]any
				if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
					t.Errorf("解析请求体失败: %v", err)
				}
				mu.Lock()
				body = decoded
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"{\"summary\":\"摘要\",\"keywords\":[\"词\"],\"topics\":[\"主题\"]}"},"finish_reason":"stop"}]}`))
			}))
			defer server.Close()
			cfg := config.Default().AI.Generation
			cfg.Profile = config.ModelProfileConfig{Provider: "test", BaseURL: server.URL, APIKey: "key", Model: "m", ProfileVersion: "v1", Timeout: time.Second}
			cfg.MaxTokensParam = tc.parameter
			cfg.MaxOutputTokens = 1234
			workflow, err := NewOpenAIWorkflow(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := workflow.Generate(context.Background(), request([]string{"正文"})); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if body[tc.want] != float64(1234) {
				t.Fatalf("%s 未按配置发送上限: %v", tc.want, body)
			}
			if _, exists := body[tc.absent]; exists {
				t.Fatalf("不应发送 %s: %v", tc.absent, body)
			}
		})
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
		{"禁止", http.StatusForbidden, 0, enrichment.ErrorAuthentication},
		{"请求非法", http.StatusBadRequest, 0, enrichment.ErrorInputUnsupported},
		{"Provider 超时", http.StatusRequestTimeout, 0, enrichment.ErrorTimeout},
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

func TestUnifiedChatAndEmbeddingErrorClassification(t *testing.T) {
	statuses := []struct {
		status    int
		want      enrichment.ErrorCode
		retryable bool
	}{
		{400, enrichment.ErrorInputUnsupported, false},
		{401, enrichment.ErrorAuthentication, false},
		{403, enrichment.ErrorAuthentication, false},
		{408, enrichment.ErrorTimeout, true},
		{429, enrichment.ErrorRateLimited, true},
		{500, enrichment.ErrorProviderUnavailable, true},
		{503, enrichment.ErrorProviderUnavailable, true},
	}
	for _, tc := range statuses {
		for name, err := range map[string]error{
			"chat_api": &modelopenai.APIError{HTTPStatusCode: tc.status, Message: "secret-key https://private.invalid/body"},
			"sdk_api":  &sdkopenai.APIError{HTTPStatusCode: tc.status, Message: "secret-key https://private.invalid/body"},
			"sdk_request": &sdkopenai.RequestError{HTTPStatusCode: tc.status, Err: errors.New("secret-key"),
				Body: []byte("https://private.invalid/body")},
		} {
			t.Run(name+"_"+http.StatusText(tc.status), func(t *testing.T) {
				got, retryable := classifyModelError(err)
				if got != tc.want || retryable != tc.retryable {
					t.Fatalf("status=%d code=%s/%t want=%s/%t", tc.status, got, retryable, tc.want, tc.retryable)
				}
				brief := safeErrorBrief(err)
				for _, secret := range []string{"secret-key", "private.invalid", "/body"} {
					if strings.Contains(brief, secret) {
						t.Fatalf("安全摘要泄露 %q: %s", secret, brief)
					}
				}
			})
		}
	}

	networkErrors := []error{
		&net.DNSError{Err: "no such host", Name: "secret.internal"},
		&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED},
		&sdkopenai.RequestError{Err: &net.DNSError{Err: "timeout", Name: "secret.internal", IsTimeout: true}, Body: []byte("secret body")},
	}
	for _, err := range networkErrors {
		if code, retryable := classifyModelError(err); code != enrichment.ErrorNetwork || !retryable {
			t.Fatalf("网络错误分类=%s/%t err=%T", code, retryable, err)
		}
		if brief := safeErrorBrief(err); strings.Contains(brief, "secret") {
			t.Fatalf("网络错误摘要泄露: %s", brief)
		}
	}
	if code, retryable := classifyModelError(context.DeadlineExceeded); code != enrichment.ErrorTimeout || !retryable {
		t.Fatalf("deadline 分类=%s/%t", code, retryable)
	}
	if code, retryable := classifyModelError(errors.New("unknown secret body")); code != enrichment.ErrorInternal || retryable {
		t.Fatalf("未知错误分类=%s/%t", code, retryable)
	}
	// 上层取消不是 Provider 故障；url.Error 同时满足 net.Error，必须先按取消分类。
	for _, canceled := range []error{context.Canceled, fmt.Errorf("调用被取消: %w", context.Canceled),
		&url.Error{Op: "Post", URL: "https://private.invalid/v1", Err: context.Canceled}} {
		code, retryable := classifyModelError(canceled)
		if code != enrichment.ErrorCanceled || !retryable {
			t.Fatalf("取消分类=%s/%t err=%T", code, retryable, canceled)
		}
		if brief := safeErrorBrief(canceled); brief != "模型调用已取消" {
			t.Fatalf("取消摘要=%q", brief)
		}
	}
}

func TestEmbeddingAdapterUsesUnifiedSDKErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want enrichment.ErrorCode
	}{
		{&sdkopenai.APIError{HTTPStatusCode: 429, Message: "secret body"}, enrichment.ErrorRateLimited},
		{&sdkopenai.RequestError{HTTPStatusCode: 403, Err: errors.New("secret"), Body: []byte("secret body")}, enrichment.ErrorAuthentication},
		{&sdkopenai.RequestError{Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}}, enrichment.ErrorNetwork},
		{context.DeadlineExceeded, enrichment.ErrorTimeout},
		{&sdkopenai.RequestError{Err: context.Canceled}, enrichment.ErrorCanceled},
	} {
		response, err := NewEmbeddingAdapter(deterministicEmbedder{err: tc.err}, 2).Embed(context.Background(), enrichment.EmbeddingRequest{Document: "正文 secret", InputHash: strings.Repeat("a", 64)})
		code, _ := enrichment.ErrorClassification(err)
		if code != tc.want || response.Call.ErrorCode != tc.want || strings.Contains(response.Call.ErrorBrief, "secret") {
			t.Fatalf("Embedding 分类=%s call=%+v want=%s", code, response.Call, tc.want)
		}
	}
}

func TestEmbeddingDimensionsRequestIsOptionalAndValidationAlwaysUsesExpectedValue(t *testing.T) {
	for _, tc := range []struct {
		name              string
		dimensions        int
		requestDimensions bool
		returned          int
		wantError         bool
	}{
		{name: "固定 1024 维省略请求参数", dimensions: 1024, returned: 1024},
		{name: "可变维数显式请求", dimensions: 2, requestDimensions: true, returned: 2},
		{name: "省略参数仍拒绝错误维度", dimensions: 2, returned: 3, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requestBody map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
					t.Error(err)
					return
				}
				vector := make([]float64, tc.returned)
				for index := range vector {
					vector[index] = float64(index+1) / float64(tc.returned+1)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "model": "embed-test", "data": []any{map[string]any{"object": "embedding", "index": 0, "embedding": vector}}})
			}))
			defer server.Close()
			cfg := config.Default().AI.Embedding
			cfg.Profile = config.ModelProfileConfig{Provider: "test", BaseURL: server.URL, APIKey: "key", Model: "embed-test", ProfileVersion: "e-v2", Timeout: time.Second}
			cfg.Dimensions = tc.dimensions
			cfg.RequestDimensions = tc.requestDimensions
			embedder, err := NewOpenAIEmbedder(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			response, err := embedder.Embed(context.Background(), enrichment.EmbeddingRequest{Document: "检索文档", InputHash: strings.Repeat("d", 64), InputVersion: "i-v1"})
			if tc.wantError {
				if code, _ := enrichment.ErrorClassification(err); code != enrichment.ErrorInvalidOutput || response.Call.ErrorReason != enrichment.ReasonVectorDimensions {
					t.Fatalf("错误维度分类=%s call=%+v err=%v", code, response.Call, err)
				}
			} else if err != nil || len(response.Vector) != tc.dimensions {
				t.Fatalf("Embedding=%d err=%v", len(response.Vector), err)
			}
			sent, exists := requestBody["dimensions"]
			if tc.requestDimensions {
				if !exists || int(sent.(float64)) != tc.dimensions {
					t.Fatalf("dimensions 未正确发送: %+v", requestBody)
				}
			} else if exists {
				t.Fatalf("固定维度模型不应发送 dimensions: %+v", requestBody)
			}
		})
	}
}

func TestGenerationStructuredOutputModesOnlyAffectFinalCalls(t *testing.T) {
	for _, tc := range []struct {
		mode     string
		wantType string
	}{
		{mode: "prompt"},
		{mode: "json_object", wantType: "json_object"},
		{mode: "json_schema", wantType: "json_schema"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			var mu sync.Mutex
			var bodies []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				bodies = append(bodies, body)
				mu.Unlock()
				messages, _ := body["messages"].([]any)
				last, _ := messages[len(messages)-1].(map[string]any)
				content := `{"summary":"摘要","keywords":["词"],"topics":["主题"]}`
				if strings.Contains(last["content"].(string), "MAP_CHUNK") {
					content = "分块摘要"
				}
				writeChatCompletion(t, w, content)
			}))
			defer server.Close()
			cfg := config.Default().AI.Generation
			cfg.StructuredOutput = tc.mode
			cfg.Profile = config.ModelProfileConfig{Provider: "test", BaseURL: server.URL, APIKey: "key", Model: "m", ProfileVersion: "v2", Timeout: time.Second}
			workflow, err := NewOpenAIWorkflow(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			result, err := workflow.Generate(context.Background(), request([]string{"第一段", "第二段"}))
			if err != nil || result.Content.Summary != "摘要" || len(bodies) != 3 {
				t.Fatalf("result=%+v bodies=%d err=%v", result, len(bodies), err)
			}
			for index, body := range bodies {
				format, exists := body["response_format"]
				if index < 2 && exists {
					t.Fatalf("Map 调用不应发送 response_format: %+v", format)
				}
				if index == 2 {
					if tc.wantType == "" && exists {
						t.Fatalf("prompt 模式不应发送 response_format: %+v", format)
					}
					if tc.wantType != "" {
						value, ok := format.(map[string]any)
						if !ok || value["type"] != tc.wantType {
							t.Fatalf("response_format=%+v", format)
						}
						if tc.mode == "json_schema" {
							schema, _ := value["json_schema"].(map[string]any)
							definition, _ := schema["schema"].(map[string]any)
							if schema["strict"] != true || definition["additionalProperties"] != false {
								t.Fatalf("JSON Schema 不完整: %+v", schema)
							}
						}
					}
				}
			}
		})
	}
}

func TestStructuredOutputProviderRejectionDoesNotDowngrade(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"unsupported response_format","type":"invalid_request_error"}}`))
	}))
	defer server.Close()
	cfg := config.Default().AI.Generation
	cfg.StructuredOutput = "json_object"
	cfg.Profile = config.ModelProfileConfig{Provider: "test", BaseURL: server.URL, APIKey: "key", Model: "m", ProfileVersion: "v2", Timeout: time.Second}
	workflow, err := NewOpenAIWorkflow(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = workflow.Generate(context.Background(), request([]string{"正文"}))
	code, _ := enrichment.ErrorClassification(err)
	if code != enrichment.ErrorInputUnsupported || calls != 1 {
		t.Fatalf("Provider 拒绝后发生降级或分类错误: code=%s calls=%d err=%v", code, calls, err)
	}
}

func TestStructuredOutputModesStillUseStrictLocalValidation(t *testing.T) {
	for _, mode := range []string{"prompt", "json_object", "json_schema"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeChatCompletion(t, w, `{"summary":"摘要","keywords":["词"],"topics":["主题"],"extra":true}`)
			}))
			defer server.Close()
			cfg := config.Default().AI.Generation
			cfg.StructuredOutput = mode
			cfg.Profile = config.ModelProfileConfig{Provider: "test", BaseURL: server.URL, APIKey: "key", Model: "m", ProfileVersion: "v2", Timeout: time.Second}
			workflow, err := NewOpenAIWorkflow(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			_, err = workflow.Generate(context.Background(), request([]string{"正文"}))
			code, _ := enrichment.ErrorClassification(err)
			if code != enrichment.ErrorInvalidOutput {
				t.Fatalf("mode=%s code=%s err=%v", mode, code, err)
			}
		})
	}
}

func writeChatCompletion(t *testing.T, w http.ResponseWriter, content string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"id": "chat", "object": "chat.completion", "created": 1, "model": "m",
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
	}); err != nil {
		t.Error(err)
	}
}
