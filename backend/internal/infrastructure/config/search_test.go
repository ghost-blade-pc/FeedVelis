package config

import (
	"strings"
	"testing"
	"time"
)

func TestSearchEnvironmentOverridesAndSchemaIdentity(t *testing.T) {
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
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	search := cfg.Search
	if !search.Enabled() || len(search.Endpoints) != 2 || search.BulkMaxItems != 250 ||
		search.Worker.Lease != 90*time.Second || search.Worker.MaxAttempts != 4 ||
		search.Rebuild.RollbackWindowD != 12*time.Hour {
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
