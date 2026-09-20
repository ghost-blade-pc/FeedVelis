package presenter

// 统一错误码常量表：同一语义只保留一个码，调用点不得再写字面量。
const (
	CodeValidationFailed      = "VALIDATION_FAILED"
	CodeNotFound              = "NOT_FOUND"
	CodeNotReady              = "NOT_READY"
	CodeInternalError         = "INTERNAL_ERROR"
	CodeInvalidCursor         = "INVALID_CURSOR"
	CodeArticleNotFound       = "ARTICLE_NOT_FOUND"
	CodeDependencyUnavailable = "DEPENDENCY_UNAVAILABLE"

	CodeInvalidCredentials   = "AUTH_INVALID_CREDENTIALS"
	CodeSessionInvalid       = "AUTH_SESSION_INVALID"
	CodeRegistrationDisabled = "AUTH_REGISTRATION_DISABLED"
	CodeCSRFRejected         = "CSRF_REJECTED"
	CodeUsernameConflict     = "AUTH_USERNAME_CONFLICT"
	CodeRateLimited          = "AUTH_RATE_LIMITED"
	CodeHashBusy             = "AUTH_HASH_BUSY"
)
