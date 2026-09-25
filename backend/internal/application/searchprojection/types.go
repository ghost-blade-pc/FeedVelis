// Package searchprojection 编排文章搜索投影：从 PostgreSQL 当前事实构造文档、
// 按物理索引投递、执行可恢复的索引重建。这里不出现 SQL、HTTP 或 OpenSearch 类型。
package searchprojection

import (
	"time"

	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
)

// ErrorCode 是投影失败的稳定分类，同时用于指标标签与持久化诊断。
// 只允许受控词表：未知分类折叠为 internal，避免标签基数无界。
type ErrorCode string

const (
	ErrorUnclassified     ErrorCode = "unclassified"
	ErrorConnection       ErrorCode = "connection"
	ErrorTimeout          ErrorCode = "timeout"
	ErrorThrottled        ErrorCode = "throttled"
	ErrorMapping          ErrorCode = "mapping"
	ErrorVersionConflict  ErrorCode = "version_conflict"
	ErrorDocumentTooLarge ErrorCode = "document_too_large"
	ErrorResponseInvalid  ErrorCode = "response_invalid"
	ErrorConfiguration    ErrorCode = "configuration"
	ErrorInternal         ErrorCode = "internal"
)

// ErrorCodes 是全部受控分类，供指标与测试核对标签集合。
var ErrorCodes = []ErrorCode{
	ErrorUnclassified, ErrorConnection, ErrorTimeout, ErrorThrottled, ErrorMapping,
	ErrorVersionConflict, ErrorDocumentTooLarge, ErrorResponseInvalid, ErrorConfiguration, ErrorInternal,
}

// Known 判断分类是否在受控词表内。
func (c ErrorCode) Known() bool {
	for _, candidate := range ErrorCodes {
		if c == candidate {
			return true
		}
	}
	return false
}

// DeliveryOutcome 是适配器对一个 delivery 的完整分类结果。
// 适配器只负责判定分类，应用层决定投递状态与重试。
type DeliveryOutcome struct {
	Result  projectionDomain.Result
	Code    ErrorCode
	Message string
}

// Normalize 把未知或缺失的错误码折叠为受控分类。
func (o DeliveryOutcome) Normalize() DeliveryOutcome {
	if !o.Code.Known() {
		o.Code = ErrorInternal
	}
	if o.Result.Applied() {
		o.Code = ErrorUnclassified
		o.Message = ""
	}
	return o
}

// StatusFor 依据分类与已用尝试次数决定投递的下一步状态。
func (o DeliveryOutcome) StatusFor(attempt, maxAttempts int) projectionDomain.Status {
	return projectionDomain.NextDeliveryStatus(o.Result, attempt, maxAttempts)
}

// Failure 返回限长、脱敏后的诊断；已收敛的结果不带诊断。
func (o DeliveryOutcome) Failure() *projectionDomain.Failure {
	normalized := o.Normalize()
	if normalized.Result.Applied() {
		return nil
	}
	return projectionDomain.NewFailure(string(normalized.Code), normalized.Message)
}

// Document 是投影到 OpenSearch 的严格映射载荷。
// 它只承载检索与过滤需要的字段：不含 Markdown、清洗 HTML、模型审计或内部错误。
type Document struct {
	ArticleID          int64
	Generation         int64
	LockVersion        int64
	RevisionID         int64
	SchemaVersion      int
	Visible            bool
	InvisibleReason    string
	OriginType         string
	SourceID           *int64
	SourceTitle        string
	AuthorUserID       string
	AuthorName         string
	PublishedAt        *time.Time
	Title              string
	PlainText          string
	Excerpt            string
	Summary            string
	Keywords           []string
	Topics             []string
	GenerationResultID string
	EmbeddingResultID  string
	Vector             []float64
	// ContentHash 是投影内容指纹：重建校验用它在不相邻的环境中比对待发布文档。
	ContentHash string
}

// Tombstone 返回不可检索的最小文档：只保留身份、版本与不可见原因。
// 下架与删除不删除物理文档，而是写入持久 tombstone，长期拒绝极迟到的旧 upsert 复活。
func (d Document) Tombstone(reason string) Document {
	return Document{
		ArticleID:       d.ArticleID,
		Generation:      d.Generation,
		LockVersion:     d.LockVersion,
		RevisionID:      d.RevisionID,
		SchemaVersion:   d.SchemaVersion,
		Visible:         false,
		InvisibleReason: reason,
		OriginType:      d.OriginType,
		SourceID:        d.SourceID,
		AuthorUserID:    d.AuthorUserID,
		PublishedAt:     d.PublishedAt,
	}
}

// CurrentProjection 是一篇文章的当前事实身份与由它构造的文档。
// 执行前复核会比较 Target 与槽位目标：不一致说明事实已变化，必须重新推进槽位。
type CurrentProjection struct {
	ArticleID int64
	Target    projectionDomain.Target
	Document  Document
	// InconsistentSelection 表示当前 Embedding 不属于当前 revision 或不依赖当前 generation 选择。
	// 此时向量已被忽略，调用方必须把它作为可观测的不一致记录，而不是写入搜索文档。
	InconsistentSelection bool
}

// InconsistentAIReason 是发现 AI 当前选择不一致时的可观测原因；
// 投影会忽略该向量而不是写入可能属于旧修订的内容。
const InconsistentAIReason = "ai_selection_inconsistent"

// ClaimedJob 是一次认领或增量扫描的结果：槽位目标、租约与需要投递的物理索引。
type ClaimedJob struct {
	ArticleID  int64
	Target     projectionDomain.Target
	Generation int64
	ChangeSeq  int64
	Attempt    int
	Lease      projectionDomain.Lease
	Deliveries []projectionDomain.Delivery
}
