// Package opensearch 是搜索投影的 OpenSearch 适配器：索引/模板/别名管理与逐项分类的 Bulk 写入。
// 它只承载协议与 SDK 细节；投影目标、状态机与重试策略都在 Application。
package opensearch

import (
	"context"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
)

// credentialsPattern 匹配 URL 中的 userinfo，用于把错误文本里的凭据替换掉。
var credentialsPattern = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]+@`)

// Config 是适配器的连接与写入边界；它由 Infrastructure 的配置组填充。
type Config struct {
	Endpoints            []string
	Username             string
	Password             string
	CAFile               string
	InsecureSkipVerify   bool
	ConnectTimeout       time.Duration
	RequestTimeout       time.Duration
	QueryTimeout         time.Duration
	IndexPrefix          string
	SchemaVersion        int
	SchemaIdentity       string
	EmbeddingDimensions  int
	BulkMaxItems         int
	BulkMaxBytes         int
	BulkMaxDocumentChars int
}

// ReadAlias 与 WriteAlias 是稳定的逻辑别名；调用方不直接引用物理索引名。
func (c Config) ReadAlias() string  { return c.IndexPrefix + "-read" }
func (c Config) WriteAlias() string { return c.IndexPrefix + "-write" }

// Client 组合 OpenSearch API 客户端与本次运行的 schema 身份。
type Client struct {
	api *opensearchapi.Client
	cfg Config
}

var (
	_ projectionApp.IndexAdmin     = (*Client)(nil)
	_ projectionApp.DocumentWriter = (*Client)(nil)
	_ articlesearch.QueryIndex     = (*Client)(nil)
)

// New 构造客户端。未配置 endpoints 时返回 ErrNotConfigured，由装配层决定禁用投影组件。
func New(cfg Config) (*Client, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, projectionApp.ErrNotConfigured
	}
	var caCert []byte
	if strings.TrimSpace(cfg.CAFile) != "" {
		data, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("读取 search.ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("search.ca_file 不含任何有效证书")
		}
		caCert = data
	}
	dialer := &net.Dialer{Timeout: cfg.ConnectTimeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		MaxIdleConnsPerHost:   8,
		ResponseHeaderTimeout: cfg.RequestTimeout,
		TLSHandshakeTimeout:   cfg.ConnectTimeout,
	}
	client, err := opensearch.NewClient(opensearch.Config{
		Addresses:          cfg.Endpoints,
		Username:           cfg.Username,
		Password:           cfg.Password,
		CACert:             caCert,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		Transport:          transport,
		RequestTimeout:     cfg.RequestTimeout,
		// 重试与退避由 delivery 的持久化策略负责：适配器只报告一次请求的真实分类。
		DisableRetry: true,
	})
	if err != nil {
		return nil, fmt.Errorf("构造 OpenSearch 客户端: %w", err)
	}
	// 不启用 errmask：逐项结果由适配器自己解析，部分失败不能被 SDK 折叠成整体错误。
	return &Client{api: opensearchapi.NewFromClient(client), cfg: cfg}, nil
}

// Close 释放底层连接。
func (c *Client) Close() error { return c.api.Close() }

// Ping 用于启动与故障恢复前的可达性检查。
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout)
	defer cancel()
	if _, err := c.api.Info(ctx, nil); err != nil {
		return redactError(err)
	}
	return nil
}

// redactError 只保留限长、无凭据的诊断，避免把请求细节带进日志与持久化错误。
func redactError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 256 {
		message = message[:256]
	}
	return &TransportError{Message: sanitizeMessage(message)}
}

// TransportError 是脱敏后的传输层错误；它不携带请求体或响应正文。
type TransportError struct{ Message string }

func (e *TransportError) Error() string { return e.Message }

// sanitizeMessage 移除内联凭据与查询串，保证错误文本可以安全记录。
func sanitizeMessage(message string) string {
	message = credentialsPattern.ReplaceAllString(message, "$1***@")
	if index := strings.IndexByte(message, '?'); index >= 0 {
		message = message[:index] + "?..."
	}
	return message
}
