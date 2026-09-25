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
	CodeSearchUnavailable     = "SEARCH_UNAVAILABLE"

	CodeInvalidCredentials   = "AUTH_INVALID_CREDENTIALS"
	CodeSessionInvalid       = "AUTH_SESSION_INVALID"
	CodeRegistrationDisabled = "AUTH_REGISTRATION_DISABLED"
	CodeCSRFRejected         = "CSRF_REJECTED"
	CodeUsernameConflict     = "AUTH_USERNAME_CONFLICT"
	CodeRateLimited          = "AUTH_RATE_LIMITED"
	CodeHashBusy             = "AUTH_HASH_BUSY"
	CodeForbidden            = "FORBIDDEN"

	CodeIdempotencyKeyRequired = "IDEMPOTENCY_KEY_REQUIRED"
	CodeIdempotencyKeyInvalid  = "IDEMPOTENCY_KEY_INVALID"
	CodeIdempotencyKeyReused   = "IDEMPOTENCY_KEY_REUSED"
	CodeIdempotencyInProgress  = "IDEMPOTENCY_IN_PROGRESS"
	CodeIfMatchRequired        = "IF_MATCH_REQUIRED"
	CodeIfMatchInvalid         = "IF_MATCH_INVALID"

	CodeArticleVersionConflict = "ARTICLE_VERSION_CONFLICT"
	CodeArticleAdminOffline    = "ARTICLE_ADMIN_OFFLINE"
	CodeArticleInvalidState    = "ARTICLE_INVALID_STATE"
	CodeArticleContentInvalid  = "ARTICLE_CONTENT_INVALID"
	CodeArticleAssetInvalid    = "ARTICLE_ASSET_INVALID"

	CodeAssetQuotaExceeded        = "ASSET_QUOTA_EXCEEDED"
	CodeAssetPendingLimitExceeded = "ASSET_PENDING_LIMIT_EXCEEDED"
	CodeAssetUnavailable          = "ASSET_UNAVAILABLE"

	CodeSourceVersionConflict = "SOURCE_VERSION_CONFLICT"
	CodeSourceAlreadyExists   = "SOURCE_ALREADY_EXISTS"
	CodeSourceFetchInProgress = "SOURCE_FETCH_IN_PROGRESS"
	CodeSourceFetchFailed     = "SOURCE_FETCH_FAILED"
)
