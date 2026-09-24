// Package enrichment 定义 AI 内容增强的确定性输入、输出、预算与应用端口。
package enrichment

import (
	"context"
	"errors"
	"time"
)

type ErrorCode string

const (
	ErrorTimeout             ErrorCode = "timeout"
	ErrorCanceled            ErrorCode = "canceled"
	ErrorRateLimited         ErrorCode = "rate_limited"
	ErrorProviderUnavailable ErrorCode = "provider_unavailable"
	ErrorNetwork             ErrorCode = "network"
	ErrorInvalidOutput       ErrorCode = "invalid_output"
	ErrorBudgetExceeded      ErrorCode = "budget_exceeded"
	ErrorInputUnsupported    ErrorCode = "input_unsupported"
	ErrorAuthentication      ErrorCode = "authentication"
	ErrorConfiguration       ErrorCode = "configuration"
	ErrorInternal            ErrorCode = "internal"
)

type ClassifiedError struct {
	Code      ErrorCode
	Retryable bool
	Reason    string
	Message   string
	Cause     error
}

func (e *ClassifiedError) Error() string { return e.Message }
func (e *ClassifiedError) Unwrap() error { return e.Cause }

func NewError(code ErrorCode, retryable bool, message string, cause error) error {
	return &ClassifiedError{Code: code, Retryable: retryable, Message: message, Cause: cause}
}

func NewErrorWithReason(code ErrorCode, retryable bool, reason, message string, cause error) error {
	return &ClassifiedError{Code: code, Retryable: retryable, Reason: reason, Message: message, Cause: cause}
}

func ErrorClassification(err error) (ErrorCode, bool) {
	var classified *ClassifiedError
	if errors.As(err, &classified) {
		return classified.Code, classified.Retryable
	}
	return ErrorInternal, false
}

// ImmediateRetryable 报告分类是否属于同一批分块内可立即重试的瞬时故障：只有传输与 Provider 侧的暂时性失败在此列，
// 非法输出等需要重新生成或纠正内容的分类仍交由任务级重试处理。
func ImmediateRetryable(code ErrorCode) bool {
	switch code {
	case ErrorTimeout, ErrorCanceled, ErrorRateLimited, ErrorProviderUnavailable, ErrorNetwork:
		return true
	default:
		return false
	}
}

func ErrorReason(err error) string {
	var classified *ClassifiedError
	if errors.As(err, &classified) {
		return classified.Reason
	}
	return ""
}

type RevisionInput struct {
	ArticleID  int64
	RevisionID int64
	Title      string
	Language   string
	PlainText  string
}

type GeneratedContent struct {
	Summary  string   `json:"summary"`
	Keywords []string `json:"keywords"`
	Topics   []string `json:"topics"`
}

type Usage struct {
	InputTokens  *int
	OutputTokens *int
	TotalTokens  *int
}

type CallRecord struct {
	Kind        string
	InputHash   string
	Usage       Usage
	Duration    time.Duration
	Status      string
	ErrorCode   ErrorCode
	ErrorBrief  string
	ErrorReason string
}

type GenerationRequest struct {
	Revision         RevisionInput
	InputHash        string
	Chunks           []string
	InputTruncated   bool
	PromptVersion    string
	WorkflowVersion  string
	MaxOutputTokens  int
	AuditTokenBudget int
	MaxCalls         int
	Concurrency      int
	MapSummaryChars  int
	TotalTimeout     time.Duration
	Limits           OutputLimits
}

type GenerationResponse struct {
	Content   GeneratedContent
	Calls     []CallRecord
	Candidate *RepairCandidate
}

type RepairCandidate struct {
	Raw    string
	Reason string
}

type RepairRequest struct {
	Candidate       RepairCandidate
	PromptVersion   string
	WorkflowVersion string
	MaxOutputTokens int
	Limits          OutputLimits
}

type Generator interface {
	Generate(context.Context, GenerationRequest) (GenerationResponse, error)
}

type OutputRepairer interface {
	Repair(context.Context, RepairRequest) (GenerationResponse, error)
}

type EmbeddingRequest struct {
	Document     string
	InputHash    string
	InputVersion string
}

type EmbeddingResponse struct {
	Vector []float64
	Call   CallRecord
}

type Embedder interface {
	Embed(context.Context, EmbeddingRequest) (EmbeddingResponse, error)
}
