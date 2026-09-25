package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// SearchConfig 描述可选的文章搜索投影与 OpenSearch 连接边界。
// endpoints 为空表示未配置：投影 Worker 不装配，文章与 AI 写路径只留下本地待处理槽位。
type SearchConfig struct {
	Endpoints           []string `yaml:"endpoints"`
	Username            string   `yaml:"username"`
	Password            string   `yaml:"password"`
	CAFile              string   `yaml:"ca_file"`
	InsecureSkipVerify  bool     `yaml:"insecure_skip_verify"`
	IndexPrefix         string   `yaml:"index_prefix"`
	SchemaVersion       int      `yaml:"schema_version"`
	EmbeddingDimensions int      `yaml:"embedding_dimensions"`

	ConnectRaw     string        `yaml:"connect_timeout"`
	ConnectTimeout time.Duration `yaml:"-"`
	RequestRaw     string        `yaml:"request_timeout"`
	RequestTimeout time.Duration `yaml:"-"`

	BulkMaxItems         int `yaml:"bulk_max_items"`
	BulkMaxBytes         int `yaml:"bulk_max_bytes"`
	BulkMaxDocumentChars int `yaml:"bulk_max_document_chars"`

	Worker  SearchWorkerConfig  `yaml:"worker"`
	Rebuild SearchRebuildConfig `yaml:"rebuild"`
}

type SearchWorkerConfig struct {
	LeaseRaw    string        `yaml:"lease"`
	Lease       time.Duration `yaml:"-"`
	PollRaw     string        `yaml:"poll_interval"`
	Poll        time.Duration `yaml:"-"`
	BatchSize   int           `yaml:"batch_size"`
	MaxAttempts int           `yaml:"max_attempts"`
	BackoffMin  string        `yaml:"backoff_min"`
	BackoffMinD time.Duration `yaml:"-"`
	BackoffMax  string        `yaml:"backoff_max"`
	BackoffMaxD time.Duration `yaml:"-"`
}

type SearchRebuildConfig struct {
	SnapshotBatch   int           `yaml:"snapshot_batch"`
	RollbackWindow  string        `yaml:"rollback_window"`
	RollbackWindowD time.Duration `yaml:"-"`
	SampleSize      int           `yaml:"validation_sample_size"`
}

// Enabled 判断是否配置了 OpenSearch 连接。
func (c SearchConfig) Enabled() bool {
	for _, endpoint := range c.Endpoints {
		if strings.TrimSpace(endpoint) != "" {
			return true
		}
	}
	return false
}

// ReadAlias 与 WriteAlias 是稳定的逻辑别名：调用方不直接引用物理索引名。
func (c SearchConfig) ReadAlias() string  { return c.IndexPrefix + "-read" }
func (c SearchConfig) WriteAlias() string { return c.IndexPrefix + "-write" }

// SchemaIdentity 描述映射、分析器、向量维度与投影编码的联合版本。
// 它与索引 _meta 中记录的值必须一致：任何一项不同都必须建立新物理索引而不是原地修改。
func (c SearchConfig) SchemaIdentity() string {
	return fmt.Sprintf("mapping-v%d|analyzer-%s|dims-%d|encoding-v%d",
		c.SchemaVersion, analyzerFor(c.SchemaVersion), c.EmbeddingDimensions, ProjectionEncodingVersion)
}

// ProjectionEncodingVersion 是文档字段编码的版本；改变字段语义时必须递增。
const ProjectionEncodingVersion = 1

// regexpIndexPrefix 限定索引前缀：OpenSearch 索引名必须是小写且不含通配字符。
var regexpIndexPrefix = regexp.MustCompile(`^[a-z][a-z0-9._-]{1,63}$`)

// analyzerFor 返回 schema 版本使用的全文分析器。
// 首版使用 OpenSearch 内置 cjk：它覆盖中英文混排且不需要维护与版本严格匹配的分析插件。
func analyzerFor(schemaVersion int) string {
	_ = schemaVersion
	return "cjk"
}

func applySearchEnvironment(cfg *Config) error {
	search := &cfg.Search
	if value, ok := os.LookupEnv("VELIS_SEARCH_ENDPOINTS"); ok {
		search.Endpoints = splitList(value)
	}
	for target, key := range map[*string]string{
		&search.Username:               "VELIS_SEARCH_USERNAME",
		&search.Password:               "VELIS_SEARCH_PASSWORD",
		&search.CAFile:                 "VELIS_SEARCH_CA_FILE",
		&search.IndexPrefix:            "VELIS_SEARCH_INDEX_PREFIX",
		&search.ConnectRaw:             "VELIS_SEARCH_CONNECT_TIMEOUT",
		&search.RequestRaw:             "VELIS_SEARCH_REQUEST_TIMEOUT",
		&search.Worker.LeaseRaw:        "VELIS_SEARCH_WORKER_LEASE",
		&search.Worker.PollRaw:         "VELIS_SEARCH_WORKER_POLL_INTERVAL",
		&search.Worker.BackoffMin:      "VELIS_SEARCH_WORKER_BACKOFF_MIN",
		&search.Worker.BackoffMax:      "VELIS_SEARCH_WORKER_BACKOFF_MAX",
		&search.Rebuild.RollbackWindow: "VELIS_SEARCH_REBUILD_ROLLBACK_WINDOW",
	} {
		setString(target, key)
	}
	for target, key := range map[*int]string{
		&search.SchemaVersion:         "VELIS_SEARCH_SCHEMA_VERSION",
		&search.EmbeddingDimensions:   "VELIS_SEARCH_EMBEDDING_DIMENSIONS",
		&search.BulkMaxItems:          "VELIS_SEARCH_BULK_MAX_ITEMS",
		&search.BulkMaxBytes:          "VELIS_SEARCH_BULK_MAX_BYTES",
		&search.BulkMaxDocumentChars:  "VELIS_SEARCH_BULK_MAX_DOCUMENT_CHARS",
		&search.Worker.BatchSize:      "VELIS_SEARCH_WORKER_BATCH_SIZE",
		&search.Worker.MaxAttempts:    "VELIS_SEARCH_WORKER_MAX_ATTEMPTS",
		&search.Rebuild.SnapshotBatch: "VELIS_SEARCH_REBUILD_SNAPSHOT_BATCH",
		&search.Rebuild.SampleSize:    "VELIS_SEARCH_REBUILD_VALIDATION_SAMPLE_SIZE",
	} {
		if err := setInt(target, key); err != nil {
			return err
		}
	}
	return setBool(&search.InsecureSkipVerify, "VELIS_SEARCH_INSECURE_SKIP_VERIFY")
}

// validateSearchConfig 校验连接边界，并核对向量维度与启用的 Embedding profile 一致。
// 维度写入映射后不可变，不一致必须通过新索引解决，不能截断、填充或静默丢弃向量。
func validateSearchConfig(search *SearchConfig, ai *AIConfig, environment string) error {
	if !search.Enabled() {
		return nil
	}
	if len(search.Endpoints) > 8 {
		return errors.New("search.endpoints 最多 8 个")
	}
	development := environment == "development" || environment == "test"
	secure := false
	for _, endpoint := range search.Endpoints {
		parsed, err := url.Parse(strings.TrimSpace(endpoint))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return errors.New("search.endpoints 必须是无凭据、查询和路径的完整 HTTP(S) URL")
		}
		if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
			return errors.New("search.endpoints 不得包含凭据、查询或路径")
		}
		if parsed.Scheme == "https" {
			secure = true
		}
	}
	if !development {
		if !secure {
			return errors.New("非开发环境 search.endpoints 必须使用 HTTPS")
		}
		if strings.TrimSpace(search.Username) == "" || search.Password == "" {
			return errors.New("非开发环境必须显式配置 search.username 与 search.password")
		}
	}
	if search.InsecureSkipVerify && !development {
		return errors.New("非开发环境不得关闭 search TLS 证书校验")
	}
	if strings.TrimSpace(search.Username) == "" && search.Password != "" {
		return errors.New("search.password 已配置但缺少 search.username")
	}
	if !regexpIndexPrefix.MatchString(search.IndexPrefix) {
		return errors.New("search.index_prefix 必须是小写字母开头的 2 到 64 位标识")
	}
	if search.SchemaVersion < 1 || search.SchemaVersion > 100 {
		return errors.New("search.schema_version 必须介于 1 和 100")
	}
	if search.EmbeddingDimensions < 1 || search.EmbeddingDimensions > 65536 {
		return errors.New("search.embedding_dimensions 必须介于 1 和 65536")
	}
	if ai.Embedding.Profile.Enabled() && ai.Embedding.Dimensions != search.EmbeddingDimensions {
		return fmt.Errorf("search.embedding_dimensions(%d) 必须与 ai.embedding.dimensions(%d) 一致；维度变更需要新索引",
			search.EmbeddingDimensions, ai.Embedding.Dimensions)
	}
	var err error
	if search.ConnectTimeout, err = durationWithin("search.connect_timeout", search.ConnectRaw, time.Second, time.Minute); err != nil {
		return err
	}
	if search.RequestTimeout, err = durationWithin("search.request_timeout", search.RequestRaw, time.Second, 5*time.Minute); err != nil {
		return err
	}
	if search.BulkMaxItems < 1 || search.BulkMaxItems > 10000 {
		return errors.New("search.bulk_max_items 必须介于 1 和 10000")
	}
	if search.BulkMaxBytes < 1024 || search.BulkMaxBytes > 64*1024*1024 {
		return errors.New("search.bulk_max_bytes 必须介于 1KiB 和 64MiB")
	}
	if search.BulkMaxDocumentChars < 1000 || search.BulkMaxDocumentChars > 4*1024*1024 {
		return errors.New("search.bulk_max_document_chars 必须介于 1000 和 4MiB")
	}
	worker := &search.Worker
	if worker.Lease, err = durationWithin("search.worker.lease", worker.LeaseRaw, 10*time.Second, 10*time.Minute); err != nil {
		return err
	}
	if worker.Poll, err = durationWithin("search.worker.poll_interval", worker.PollRaw, 100*time.Millisecond, time.Minute); err != nil {
		return err
	}
	if worker.BackoffMinD, err = durationWithin("search.worker.backoff_min", worker.BackoffMin, time.Second, time.Hour); err != nil {
		return err
	}
	if worker.BackoffMaxD, err = durationWithin("search.worker.backoff_max", worker.BackoffMax, time.Second, time.Hour); err != nil {
		return err
	}
	if worker.BackoffMinD > worker.BackoffMaxD {
		return errors.New("search.worker.backoff_min 不得大于 backoff_max")
	}
	if worker.BatchSize < 1 || worker.BatchSize > 500 {
		return errors.New("search.worker.batch_size 必须介于 1 和 500")
	}
	if worker.MaxAttempts < 1 || worker.MaxAttempts > 20 {
		return errors.New("search.worker.max_attempts 必须介于 1 和 20")
	}
	if worker.Lease <= search.RequestTimeout {
		return errors.New("search.worker.lease 必须大于 search.request_timeout")
	}
	rebuild := &search.Rebuild
	if rebuild.RollbackWindowD, err = durationWithin("search.rebuild.rollback_window", rebuild.RollbackWindow, time.Minute, 30*24*time.Hour); err != nil {
		return err
	}
	if rebuild.SnapshotBatch < 1 || rebuild.SnapshotBatch > 10000 {
		return errors.New("search.rebuild.snapshot_batch 必须介于 1 和 10000")
	}
	if rebuild.SampleSize < 1 || rebuild.SampleSize > 10000 {
		return errors.New("search.rebuild.validation_sample_size 必须介于 1 和 10000")
	}
	return nil
}

// RedactedSearchEndpoints 只保留协议与主机，供日志与启动信息使用。
func RedactedSearchEndpoints(endpoints []string) []string {
	redacted := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		parsed, err := url.Parse(strings.TrimSpace(endpoint))
		if err != nil || parsed.Host == "" {
			redacted = append(redacted, "invalid")
			continue
		}
		redacted = append(redacted, parsed.Scheme+"://"+parsed.Host)
	}
	return redacted
}
