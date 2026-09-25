package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSearchEnvironmentOverridesAndSchemaIdentity(t *testing.T) {
	cursorKey := []byte("0123456789abcdef0123456789abcdef")
	t.Setenv("VELIS_SEARCH_ENDPOINTS", "https://search.internal:9200, https://search-2.internal:9200")
	t.Setenv("VELIS_SEARCH_USERNAME", "velis")
	t.Setenv("VELIS_SEARCH_PASSWORD", "secret-value")
	t.Setenv("VELIS_SEARCH_INDEX_PREFIX", "velis-articles")
	t.Setenv("VELIS_SEARCH_SCHEMA_VERSION", "3")
	t.Setenv("VELIS_SEARCH_EMBEDDING_DIMENSIONS", "768")
	t.Setenv("VELIS_SEARCH_BULK_MAX_ITEMS", "250")
	t.Setenv("VELIS_SEARCH_WORKER_LEASE", "90s")
	t.Setenv("VELIS_SEARCH_WORKER_MAX_ATTEMPTS", "4")
	t.Setenv("VELIS_SEARCH_REBUILD_ROLLBACK_WINDOW", "12h")
	t.Setenv("VELIS_SEARCH_QUERY_TIMEOUT", "4s")
	t.Setenv("VELIS_SEARCH_QUERY_PIT_KEEP_ALIVE", "3m")
	t.Setenv("VELIS_SEARCH_QUERY_CANDIDATE_BATCH_SIZE", "120")
	t.Setenv("VELIS_SEARCH_QUERY_MAX_CANDIDATES_PER_REQUEST", "600")
	t.Setenv("VELIS_SEARCH_CURSOR_KEY", base64.StdEncoding.EncodeToString(cursorKey))
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	search := cfg.Search
	if !search.Enabled() || len(search.Endpoints) != 2 || search.BulkMaxItems != 250 ||
		search.Worker.Lease != 90*time.Second || search.Worker.MaxAttempts != 4 ||
		search.Rebuild.RollbackWindowD != 12*time.Hour || search.Query.Timeout != 4*time.Second ||
		search.Query.PITKeepAlive != 3*time.Minute || search.Query.CandidateBatchSize != 120 ||
		search.Query.MaxCandidatesPerRequest != 600 || !search.QueryEnabled() {
		t.Fatalf("Search 环境覆盖失败: %+v", search)
	}
	if search.ReadAlias() != "velis-articles-read" || search.WriteAlias() != "velis-articles-write" {
		t.Fatalf("别名必须由前缀派生: %s/%s", search.ReadAlias(), search.WriteAlias())
	}
	identity := search.SchemaIdentity()
	for _, fragment := range []string{"mapping-v3", "analyzer-cjk", "dims-768", "encoding-v1"} {
		if !strings.Contains(identity, fragment) {
			t.Fatalf("schema identity 必须包含 %s: %s", fragment, identity)
		}
	}
}

func TestSearchQueryCursorKeyIsEnvironmentOnlyAndRedacted(t *testing.T) {
	key := []byte("cursor-secret-0123456789abcdef0123")
	t.Setenv("VELIS_SEARCH_ENDPOINTS", "http://localhost:9200")
	t.Setenv("VELIS_SEARCH_CURSOR_KEY", base64.StdEncoding.EncodeToString(key))
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("search:\n  query:\n    cursor_key: yaml-must-not-win\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(cfg.Search.Query.CursorKey.Bytes()); got != string(key) {
		t.Fatalf("cursor key 未从环境读取: %q", got)
	}
	formatted := fmt.Sprintf("%+v %#v", cfg.Search, cfg.Search.Query.CursorKey)
	for _, leaked := range []string{string(key), base64.StdEncoding.EncodeToString(key), "yaml-must-not-win"} {
		if strings.Contains(formatted, leaked) {
			t.Fatalf("配置格式化泄露游标签名键 %q: %s", leaked, formatted)
		}
	}
}

func TestSearchQueryCursorKeyAvailabilityByEnvironment(t *testing.T) {
	development := Default()
	development.Search.Endpoints = []string{"http://localhost:9200"}
	if err := development.Validate(); err != nil {
		t.Fatal(err)
	}
	if !development.Search.QueryEnabled() || !development.Search.Query.CursorKeyEphemeral {
		t.Fatal("development 应生成仅驻留内存的临时 cursor key")
	}

	production := Default()
	production.App.Environment = "production"
	production.Search.Endpoints = []string{"https://search.internal:9200"}
	production.Search.Username, production.Search.Password = "reader", "secret"
	if err := production.Validate(); err != nil {
		t.Fatalf("生产缺少 cursor key 只能禁用查询装配，不得阻止核心配置加载: %v", err)
	}
	if production.Search.QueryEnabled() || production.Search.Query.CursorKey.IsSet() {
		t.Fatal("生产缺少 cursor key 时查询能力必须保持禁用")
	}
}

func TestSearchQueryRejectsInvalidKeyAndBounds(t *testing.T) {
	t.Setenv("VELIS_SEARCH_CURSOR_KEY", "not-base64")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "VELIS_SEARCH_CURSOR_KEY") {
		t.Fatalf("非法 cursor key 应返回脱敏配置错误: %v", err)
	}

	cases := []func(*Config){
		func(cfg *Config) { cfg.Search.Query.TimeoutRaw = "50ms" },
		func(cfg *Config) { cfg.Search.Query.PITKeepAliveRaw = "11m" },
		func(cfg *Config) { cfg.Search.Query.CandidateBatchSize = 0 },
		func(cfg *Config) { cfg.Search.Query.MaxCandidatesPerRequest = 99 },
		func(cfg *Config) { cfg.Search.Query.MaxCandidatesPerRequest = 5001 },
	}
	for index, mutate := range cases {
		cfg := Default()
		cfg.Search.Endpoints = []string{"http://localhost:9200"}
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Fatalf("case %d 应拒绝", index)
		}
	}
}

func TestSearchDisabledSkipsConnectionValidation(t *testing.T) {
	// 默认配置没有 endpoints：即使其他字段是默认值也必须视为「未配置」而不是无效。
	cfg := Default()
	if cfg.Search.Enabled() {
		t.Fatal("默认配置不应启用搜索投影")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("未配置 OpenSearch 时不得校验连接边界: %v", err)
	}
	cfg.Search.IndexPrefix = "INVALID Prefix"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("未配置时索引前缀也不参与校验: %v", err)
	}
}

func TestSearchRejectsUnsafeEndpointsAndCredentials(t *testing.T) {
	cases := map[string]func(*Config){
		"明文 HTTP 用于生产": func(cfg *Config) {
			cfg.App.Environment = "production"
			cfg.Search.Endpoints = []string{"http://search.internal:9200"}
			cfg.Search.Username, cfg.Search.Password = "velis", "secret"
		},
		"生产缺少凭据": func(cfg *Config) {
			cfg.App.Environment = "production"
			cfg.Search.Endpoints = []string{"https://search.internal:9200"}
		},
		"endpoint 携带凭据": func(cfg *Config) {
			cfg.Search.Endpoints = []string{"https://user:pass@search.internal:9200"}
		},
		"endpoint 携带查询": func(cfg *Config) {
			cfg.Search.Endpoints = []string{"https://search.internal:9200?token=hidden"}
		},
		"endpoint 携带路径": func(cfg *Config) {
			cfg.Search.Endpoints = []string{"https://search.internal:9200/proxy"}
		},
		"缺少协议": func(cfg *Config) {
			cfg.Search.Endpoints = []string{"search.internal:9200"}
		},
		"非开发环境关闭证书校验": func(cfg *Config) {
			cfg.App.Environment = "production"
			cfg.Search.Endpoints = []string{"https://search.internal:9200"}
			cfg.Search.Username, cfg.Search.Password = "velis", "secret"
			cfg.Search.InsecureSkipVerify = true
		},
		"只有密码没有用户名": func(cfg *Config) {
			cfg.Search.Endpoints = []string{"http://localhost:9200"}
			cfg.Search.Password = "secret"
		},
	}
	for name, mutate := range cases {
		cfg := Default()
		cfg.Search.IndexPrefix = "velis-articles"
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Fatalf("%s: 期望校验失败", name)
		}
	}
}

func TestSearchRejectsOutOfRangeBoundaries(t *testing.T) {
	cases := map[string]func(*Config){
		"schema 版本越界":    func(cfg *Config) { cfg.Search.SchemaVersion = 0 },
		"向量维度越界":         func(cfg *Config) { cfg.Search.EmbeddingDimensions = 0 },
		"Bulk item 上限越界": func(cfg *Config) { cfg.Search.BulkMaxItems = 0 },
		"Bulk 字节上限越界":    func(cfg *Config) { cfg.Search.BulkMaxBytes = 512 },
		"单文档字符上限越界":      func(cfg *Config) { cfg.Search.BulkMaxDocumentChars = 10 },
		"索引前缀非法":         func(cfg *Config) { cfg.Search.IndexPrefix = "Velis" },
		"尝试次数越界":         func(cfg *Config) { cfg.Search.Worker.MaxAttempts = 0 },
		"批大小越界":          func(cfg *Config) { cfg.Search.Worker.BatchSize = 0 },
		"退避上界小于下界":       func(cfg *Config) { cfg.Search.Worker.BackoffMin, cfg.Search.Worker.BackoffMax = "10m", "1s" },
		"租约不长于请求超时":      func(cfg *Config) { cfg.Search.Worker.LeaseRaw = "5s" },
		"回滚窗口过短":         func(cfg *Config) { cfg.Search.Rebuild.RollbackWindow = "1s" },
		"抽样数量越界":         func(cfg *Config) { cfg.Search.Rebuild.SampleSize = 0 },
		"endpoint 过多": func(cfg *Config) {
			cfg.Search.Endpoints = []string{"http://a:9200", "http://b:9200", "http://c:9200", "http://d:9200",
				"http://e:9200", "http://f:9200", "http://g:9200", "http://h:9200", "http://i:9200"}
		},
	}
	for name, mutate := range cases {
		cfg := Default()
		cfg.App.Environment = "development"
		cfg.Search.Endpoints = []string{"http://localhost:9200"}
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Fatalf("%s: 期望校验失败", name)
		}
	}
}

func TestSearchVectorDimensionsMustMatchEnabledEmbeddingProfile(t *testing.T) {
	cfg := Default()
	cfg.App.Environment = "development"
	cfg.Search.Endpoints = []string{"http://localhost:9200"}
	cfg.Search.EmbeddingDimensions = 1024
	cfg.AI.Embedding.Profile = ModelProfileConfig{
		Provider: "openai-compatible", BaseURL: "https://models.internal/v1", APIKey: "key",
		Model: "embed-3", ProfileVersion: "embedding-v2", TimeoutRaw: "20s", BudgetRaw: "30s",
	}
	cfg.AI.Embedding.Dimensions = 768
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "必须与 ai.embedding.dimensions") {
		t.Fatalf("维度不一致必须暴露配置错误: %v", err)
	}
	// 维度必须通过新索引解决，不能通过截断或填充绕过。
	cfg.Search.EmbeddingDimensions = 768
	if err := cfg.Validate(); err != nil {
		t.Fatalf("维度一致时必须通过: %v", err)
	}
}

func TestRedactedSearchEndpointsDropsCredentialsQueryAndPath(t *testing.T) {
	redacted := RedactedSearchEndpoints([]string{
		"https://user:secret@search.internal:9200",
		"https://search-2.internal:9243?token=hidden#frag",
		"not-a-url",
	})
	if redacted[0] != "https://search.internal:9200" {
		t.Fatalf("必须去除 userinfo: %s", redacted[0])
	}
	if redacted[1] != "https://search-2.internal:9243" {
		t.Fatalf("必须去除 query 与 fragment: %s", redacted[1])
	}
	if redacted[2] != "invalid" {
		t.Fatalf("非法 endpoint 只能记录无效标记: %s", redacted[2])
	}
	for _, value := range redacted {
		for _, secret := range []string{"secret", "hidden", "frag", "token"} {
			if strings.Contains(value, secret) {
				t.Fatalf("脱敏结果泄露 %q: %s", secret, value)
			}
		}
	}
}

func TestComposeInjectsSearchQueryIntoAPIWithoutCommittedCursorKey(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", "..", ".."))
	compose, err := os.ReadFile(filepath.Join(root, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(compose)
	for _, required := range []string{
		"VELIS_HTTP_METRICS_ADDRESS: 0.0.0.0:9090",
		"VELIS_SEARCH_ENDPOINTS: ${VELIS_SEARCH_ENDPOINTS-http://opensearch:9200}",
		"VELIS_SEARCH_CURSOR_KEY: ${VELIS_SEARCH_CURSOR_KEY-}",
		"VELIS_SEARCH_QUERY_TIMEOUT: ${VELIS_SEARCH_QUERY_TIMEOUT-3s}",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("Compose 缺少 API 搜索配置 %q", required)
		}
	}
	if strings.Contains(text, "VELIS_SEARCH_CURSOR_KEY: "+base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))) {
		t.Fatal("Compose 不得提交 cursor key 示例值")
	}
	example, err := os.ReadFile(filepath.Join(root, "backend", "configs", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(example), "cursor_key:") {
		t.Fatal("YAML 示例不得提供 cursor_key 字段")
	}
}
