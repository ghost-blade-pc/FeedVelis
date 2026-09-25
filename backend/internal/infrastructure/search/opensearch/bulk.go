package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
)

// documentBody 是严格映射的文档载荷：字段与模板一一对应，未列出的字段一律不发送。
type documentBody struct {
	ArticleID             int64      `json:"article_id"`
	ProjectionGeneration  int64      `json:"projection_generation"`
	LockVersion           int64      `json:"lock_version"`
	RevisionID            int64      `json:"revision_id"`
	SchemaVersion         int        `json:"schema_version"`
	Visible               bool       `json:"visible"`
	InvisibleReason       string     `json:"invisible_reason,omitempty"`
	OriginType            string     `json:"origin_type"`
	SourceID              *int64     `json:"source_id,omitempty"`
	AuthorUserID          string     `json:"author_user_id,omitempty"`
	PublishedAt           *time.Time `json:"published_at,omitempty"`
	SourceTitle           string     `json:"source_title,omitempty"`
	AuthorName            string     `json:"author_name,omitempty"`
	Title                 string     `json:"title,omitempty"`
	PlainText             string     `json:"plain_text,omitempty"`
	Excerpt               string     `json:"excerpt,omitempty"`
	Summary               string     `json:"summary,omitempty"`
	Keywords              []string   `json:"keywords,omitempty"`
	Topics                []string   `json:"topics,omitempty"`
	GenerationResultID    string     `json:"generation_result_id,omitempty"`
	EmbeddingResultID     string     `json:"embedding_result_id,omitempty"`
	ProjectionContentHash string     `json:"projection_content_hash,omitempty"`
	Vector                []float64  `json:"vector,omitempty"`
}

func renderDocument(document projectionApp.Document) ([]byte, error) {
	return json.Marshal(documentBody{
		ArticleID: document.ArticleID, ProjectionGeneration: document.Generation,
		LockVersion: document.LockVersion, RevisionID: document.RevisionID,
		SchemaVersion: document.SchemaVersion, Visible: document.Visible,
		InvisibleReason: document.InvisibleReason, OriginType: document.OriginType,
		SourceID: document.SourceID, AuthorUserID: document.AuthorUserID,
		PublishedAt: document.PublishedAt, SourceTitle: document.SourceTitle,
		AuthorName: document.AuthorName, Title: document.Title, PlainText: document.PlainText,
		Excerpt: document.Excerpt, Summary: document.Summary, Keywords: document.Keywords,
		Topics: document.Topics, GenerationResultID: document.GenerationResultID,
		EmbeddingResultID: document.EmbeddingResultID, Vector: document.Vector,
		ProjectionContentHash: document.ContentHash,
	})
}

// bulkBatch 是一次 HTTP Bulk 请求的输入：同批次只写一个物理索引。
type bulkBatch struct {
	index     string
	positions []int
	items     []projectionApp.BulkItem
}

// Bulk 按物理索引、item 数与请求字节切分后逐批写入。
// 返回的分类与 items 按下标一一对应：连接结果未知与限流都是可重试分类，
// 只有严格映射失败、超大文档或非法响应才是永久失败。
func (c *Client) Bulk(ctx context.Context, items []projectionApp.BulkItem) ([]projectionApp.DeliveryOutcome, error) {
	outcomes := make([]projectionApp.DeliveryOutcome, len(items))
	pending := make([]int, 0, len(items))
	for index, item := range items {
		if c.oversized(item.Document) {
			// 超限文档单独永久失败，不拖累同批其它文档。
			outcomes[index] = projectionApp.DeliveryOutcome{
				Result: projectionDomain.ResultPermanent, Code: projectionApp.ErrorDocumentTooLarge,
				Message: "文档超过允许的投影载荷上限",
			}
			continue
		}
		pending = append(pending, index)
	}
	for _, batch := range c.batches(items, pending) {
		results := c.sendBatch(ctx, batch)
		for offset, position := range batch.positions {
			outcomes[position] = results[offset]
		}
	}
	return outcomes, nil
}

func (c *Client) oversized(document projectionApp.Document) bool {
	if c.cfg.BulkMaxDocumentChars <= 0 {
		return false
	}
	return len(document.PlainText)+len(document.Summary) > c.cfg.BulkMaxDocumentChars
}

// batches 按物理索引与上限切分待写文档，保持原始顺序。
func (c *Client) batches(items []projectionApp.BulkItem, pending []int) []bulkBatch {
	batches := make([]bulkBatch, 0)
	current := bulkBatch{}
	currentBytes := 0
	flush := func() {
		if len(current.items) > 0 {
			batches = append(batches, current)
			current = bulkBatch{}
			currentBytes = 0
		}
	}
	for _, position := range pending {
		item := items[position]
		body, err := renderDocument(item.Document)
		if err != nil {
			continue
		}
		if current.index != "" && (current.index != item.PhysicalIndex ||
			len(current.items) >= c.cfg.BulkMaxItems || currentBytes+len(body) > c.cfg.BulkMaxBytes) {
			flush()
		}
		current.index = item.PhysicalIndex
		current.items = append(current.items, item)
		current.positions = append(current.positions, position)
		currentBytes += len(body) + 128
	}
	flush()
	return batches
}

// sendBatch 执行一次 Bulk 请求，并逐项关联响应。
func (c *Client) sendBatch(ctx context.Context, batch bulkBatch) []projectionApp.DeliveryOutcome {
	if len(batch.items) == 0 {
		return nil
	}
	body, err := buildBulkBody(batch.index, batch.items)
	if err != nil {
		return failures(len(batch.items), projectionApp.ErrorInternal, "构造 Bulk 请求失败")
	}
	header := http.Header{}
	// Bulk 正文字协议是 NDJSON；缺少该头会让服务端按 JSON 解析而整体失败。
	header.Set("Content-Type", "application/x-ndjson")
	response, callErr := c.api.Bulk(ctx, opensearchapi.BulkReq{
		Index: batch.index, Body: bytes.NewReader(body), Header: header,
		Params: opensearchapi.BulkParams{Timeout: c.cfg.RequestTimeout},
	})
	if callErr != nil || response == nil {
		// 连接在服务端处理期间中断时结果未知：保留任务并允许幂等重试。
		return failures(len(batch.items), classifyTransportError(callErr), "Bulk 请求结果未知，将幂等重试")
	}
	if len(response.Items) != len(batch.items) {
		// 响应缺项或项数不符时整批无法可靠关联。
		return failures(len(batch.items), projectionApp.ErrorResponseInvalid, "Bulk 响应项数与请求不一致")
	}
	outcomes := make([]projectionApp.DeliveryOutcome, len(batch.items))
	for index, item := range batch.items {
		outcomes[index] = classifyBulkItem(batch.index, item, response.Items[index])
	}
	return outcomes
}

func failures(count int, code projectionApp.ErrorCode, message string) []projectionApp.DeliveryOutcome {
	outcomes := make([]projectionApp.DeliveryOutcome, count)
	for index := range outcomes {
		outcomes[index] = projectionApp.DeliveryOutcome{
			Result: projectionDomain.ResultUnknown, Code: code, Message: message,
		}
	}
	return outcomes
}

// buildBulkBody 构造 NDJSON：动作行携带文章 ID 与 generation 作为 external_gte 外部版本。
// 同 generation 的重放是安全的，更低 generation 会被 OpenSearch 拒绝。
func buildBulkBody(index string, items []projectionApp.BulkItem) ([]byte, error) {
	var buffer bytes.Buffer
	for _, item := range items {
		action := map[string]any{
			"_index": index, "_id": strconv.FormatInt(item.Document.ArticleID, 10),
		}
		if item.Document.Generation > 0 {
			action["version"] = item.Document.Generation
			action["version_type"] = "external_gte"
		}
		actionLine, err := json.Marshal(map[string]any{"index": action})
		if err != nil {
			return nil, err
		}
		documentLine, err := renderDocument(item.Document)
		if err != nil {
			return nil, err
		}
		buffer.Write(actionLine)
		buffer.WriteByte('\n')
		buffer.Write(documentLine)
		buffer.WriteByte('\n')
	}
	return buffer.Bytes(), nil
}

// classifyBulkItem 把一个 Bulk item 映射为受控分类。
func classifyBulkItem(index string, item projectionApp.BulkItem, response map[string]opensearchapi.BulkRespItem) projectionApp.DeliveryOutcome {
	entry, ok := singleAction(response)
	if !ok {
		return projectionApp.DeliveryOutcome{Result: projectionDomain.ResultUnknown,
			Code: projectionApp.ErrorResponseInvalid, Message: "Bulk item 动作无法关联"}
	}
	if entry.ID != strconv.FormatInt(item.Document.ArticleID, 10) || (entry.Index != "" && entry.Index != index) {
		return projectionApp.DeliveryOutcome{Result: projectionDomain.ResultUnknown,
			Code: projectionApp.ErrorResponseInvalid, Message: "Bulk item 身份与请求不一致"}
	}
	if entry.Error != nil {
		return classifyBulkError(entry.Status, entry.Error.Type, entry.Error.Reason)
	}
	if entry.Status < 200 || entry.Status > 299 {
		return projectionApp.DeliveryOutcome{Result: projectionDomain.ResultRetryable,
			Code: projectionApp.ErrorConnection, Message: "Bulk item 返回非成功状态"}
	}
	switch entry.Result {
	case "noop":
		return projectionApp.DeliveryOutcome{Result: projectionDomain.ResultNoop}
	case "created":
		return projectionApp.DeliveryOutcome{Result: appliedResult(item)}
	default:
		return projectionApp.DeliveryOutcome{Result: appliedResult(item)}
	}
}

// appliedResult 区分 tombstone 与可检索文档的写入结果，便于诊断。
func appliedResult(item projectionApp.BulkItem) projectionDomain.Result {
	if !item.Document.Visible {
		return projectionDomain.ResultTombstoned
	}
	return projectionDomain.ResultUpdated
}

// classifyBulkError 把 OpenSearch 的错误类型映射为稳定分类。
// 版本冲突说明远端文档版本不低于请求版本：陈旧写入必须视为已收敛，而不是失败。
func classifyBulkError(status int, errorType, reason string) projectionApp.DeliveryOutcome {
	message := sanitizeMessage(strings.TrimSpace(errorType + ": " + reason))
	switch {
	case status == http.StatusConflict || errorType == "version_conflict_engine_exception":
		return projectionApp.DeliveryOutcome{Result: projectionDomain.ResultNoop}
	case errorType == "mapper_parsing_exception" || errorType == "strict_dynamic_mapping_exception" ||
		errorType == "illegal_argument_exception" || errorType == "document_parsing_exception":
		return projectionApp.DeliveryOutcome{Result: projectionDomain.ResultPermanent, Code: projectionApp.ErrorMapping, Message: message}
	case status == http.StatusRequestEntityTooLarge || errorType == "document_too_large":
		return projectionApp.DeliveryOutcome{Result: projectionDomain.ResultPermanent, Code: projectionApp.ErrorDocumentTooLarge, Message: message}
	case status == http.StatusTooManyRequests || errorType == "es_rejected_execution_exception":
		return projectionApp.DeliveryOutcome{Result: projectionDomain.ResultRetryable, Code: projectionApp.ErrorThrottled, Message: message}
	case status == http.StatusRequestTimeout:
		return projectionApp.DeliveryOutcome{Result: projectionDomain.ResultRetryable, Code: projectionApp.ErrorTimeout, Message: message}
	case status >= 500:
		return projectionApp.DeliveryOutcome{Result: projectionDomain.ResultRetryable, Code: projectionApp.ErrorConnection, Message: message}
	default:
		return projectionApp.DeliveryOutcome{Result: projectionDomain.ResultPermanent, Code: projectionApp.ErrorMapping, Message: message}
	}
}

// singleAction 要求 item 恰好含一个动作键，否则无法可靠关联。
func singleAction(item map[string]opensearchapi.BulkRespItem) (opensearchapi.BulkRespItem, bool) {
	if len(item) != 1 {
		return opensearchapi.BulkRespItem{}, false
	}
	for _, entry := range item {
		return entry, true
	}
	return opensearchapi.BulkRespItem{}, false
}

// classifyTransportError 把传输层错误映射为受控分类：结果未知一律可重试。
func classifyTransportError(err error) projectionApp.ErrorCode {
	if err == nil {
		return projectionApp.ErrorConnection
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "timeout") || strings.Contains(message, "deadline exceeded"):
		return projectionApp.ErrorTimeout
	case strings.Contains(message, "429") || strings.Contains(message, "too many requests"):
		return projectionApp.ErrorThrottled
	default:
		return projectionApp.ErrorConnection
	}
}
