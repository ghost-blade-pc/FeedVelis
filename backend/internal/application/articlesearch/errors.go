package articlesearch

import "errors"

type ErrorCode string

const (
	CodeValidationFailed      ErrorCode = "VALIDATION_FAILED"
	CodeInvalidCursor         ErrorCode = "INVALID_CURSOR"
	CodeSearchUnavailable     ErrorCode = "SEARCH_UNAVAILABLE"
	CodeDependencyUnavailable ErrorCode = "DEPENDENCY_UNAVAILABLE"
	CodeInternal              ErrorCode = "INTERNAL_ERROR"
)

type Error struct {
	Code  ErrorCode
	Cause error
}

func (e *Error) Error() string { return string(e.Code) }
func (e *Error) Unwrap() error { return e.Cause }

func controlled(code ErrorCode, cause error) error { return &Error{Code: code, Cause: cause} }

func CodeOf(err error) ErrorCode {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return CodeInternal
}

var (
	ErrPITNotFound          = errors.New("搜索快照不存在")
	ErrIndexUnavailable     = errors.New("搜索索引不可用")
	ErrInvalidIndexResponse = errors.New("搜索响应无效")
)
