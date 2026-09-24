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
	Message   string
	Cause     error
}

func (e *ClassifiedError) Error() string { return e.Message }
func (e *ClassifiedError) Unwrap() error { return e.Cause }

func NewError(code ErrorCode, retryable bool, message string, cause error) error {
	return &ClassifiedError{Code: code, Retryable: retryable, Message: message, Cause: cause}
}

func ErrorClassification(err error) (ErrorCode, bool) {
	var classified *ClassifiedError
	if errors.As(err, &classified) {
		return classified.Code, classified.Retryable
	}
	return ErrorInternal, false
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
	Kind       string
	InputHash  string
	Usage      Usage
	Duration   time.Duration
	Status     string
	ErrorCode  ErrorCode
	ErrorBrief string
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
	TotalTimeout     time.Duration
	Limits           OutputLimits
}

type GenerationResponse struct {
	Content GeneratedContent
	Calls   []CallRecord
}

type Generator interface {
	Generate(context.Context, GenerationRequest) (GenerationResponse, error)
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
