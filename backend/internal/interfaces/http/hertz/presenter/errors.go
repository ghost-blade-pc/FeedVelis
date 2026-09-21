package presenter

import (
	"errors"
	"strconv"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	assetApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asset"
	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	sourceApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/source"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
	assetDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/asset"
	sourceDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/source"
)

// ErrorMapping 是错误到 HTTP 响应的映射结果；RetryAfter 大于 0 时写出 Retry-After 头。
type ErrorMapping struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
}

// MapAuthError 映射认证相关错误。
// 未识别的错误按依赖不可用处理：数据库、散列与令牌依赖故障时拒绝访问，而不是回退到仅信任令牌。
func MapAuthError(err error) ErrorMapping {
	var rateLimited *accountApp.RateLimitedError
	switch {
	case errors.As(err, &rateLimited):
		return ErrorMapping{Status: consts.StatusTooManyRequests, Code: CodeRateLimited,
			Message: "登录尝试过于频繁，请稍后重试", RetryAfter: rateLimited.RetryAfter}
	case errors.Is(err, ports.ErrHasherBusy):
		return ErrorMapping{Status: consts.StatusServiceUnavailable, Code: CodeHashBusy,
			Message: "服务繁忙，请稍后重试", RetryAfter: time.Second}
	case errors.Is(err, accountApp.ErrRegistrationDisabled):
		return ErrorMapping{Status: consts.StatusForbidden, Code: CodeRegistrationDisabled, Message: "注册入口已关闭"}
	case errors.Is(err, accountApp.ErrInvalidCredentials):
		return ErrorMapping{Status: consts.StatusUnauthorized, Code: CodeInvalidCredentials, Message: "用户名或密码错误"}
	case errors.Is(err, accountApp.ErrUnauthorized),
		errors.Is(err, accountDomain.ErrSessionInvalid),
		errors.Is(err, accountDomain.ErrRefreshUnknown),
		errors.Is(err, accountDomain.ErrRefreshReplay):
		return ErrorMapping{Status: consts.StatusUnauthorized, Code: CodeSessionInvalid, Message: "登录状态无效，请重新登录"}
	case errors.Is(err, accountDomain.ErrUsernameTaken):
		return ErrorMapping{Status: consts.StatusConflict, Code: CodeUsernameConflict, Message: "用户名已存在"}
	case errors.Is(err, accountDomain.ErrVersionConflict):
		return ErrorMapping{Status: consts.StatusConflict, Code: CodeValidationFailed, Message: "账户已被其他操作修改，请刷新后重试"}
	case errors.Is(err, accountDomain.ErrNotFound):
		return ErrorMapping{Status: consts.StatusNotFound, Code: CodeNotFound, Message: "账户不存在"}
	case errors.Is(err, accountDomain.ErrInvalidUsername),
		errors.Is(err, accountDomain.ErrInvalidNickname),
		errors.Is(err, accountDomain.ErrPasswordLength),
		errors.Is(err, accountDomain.ErrPasswordCharset),
		errors.Is(err, accountDomain.ErrPasswordClasses),
		errors.Is(err, accountDomain.ErrPasswordTooCommon),
		errors.Is(err, accountDomain.ErrPasswordEqualsUsername),
		errors.Is(err, accountDomain.ErrInvalidRole),
		errors.Is(err, accountDomain.ErrInvalidStatus),
		errors.Is(err, accountDomain.ErrInvalidUUID),
		errors.Is(err, ErrMalformedBody),
		errors.Is(err, ErrBodyTooLarge):
		return ErrorMapping{Status: consts.StatusBadRequest, Code: CodeValidationFailed, Message: err.Error()}
	default:
		return ErrorMapping{Status: consts.StatusServiceUnavailable, Code: CodeDependencyUnavailable,
			Message: "认证依赖暂不可用"}
	}
}

// MapContentError 映射文章、资产与幂等写命令的错误。
// 头部解析错误与内容校验错误一并在此收敛，使所有 I2 写接口返回相同的状态码和错误码。
// 未识别的错误按依赖不可用处理：数据库或渲染依赖故障时不把失败报告成校验问题。
func MapContentError(err error) ErrorMapping {
	var rateLimited *accountApp.RateLimitedError
	switch {
	case errors.Is(err, ErrIdempotencyKeyRequired):
		return ErrorMapping{Status: consts.StatusBadRequest, Code: CodeIdempotencyKeyRequired, Message: "缺少 Idempotency-Key 请求头"}
	case errors.Is(err, ErrIdempotencyKeyInvalid):
		return ErrorMapping{Status: consts.StatusBadRequest, Code: CodeIdempotencyKeyInvalid, Message: "Idempotency-Key 必须是 UUID"}
	case errors.Is(err, ErrIfMatchRequired):
		return ErrorMapping{Status: consts.StatusBadRequest, Code: CodeIfMatchRequired, Message: "缺少 If-Match 请求头"}
	case errors.Is(err, ErrIfMatchInvalid):
		return ErrorMapping{Status: consts.StatusBadRequest, Code: CodeIfMatchInvalid, Message: "If-Match 必须是双引号包裹的当前 lock_version"}
	case errors.Is(err, idempotencyApp.ErrKeyReused):
		return ErrorMapping{Status: consts.StatusConflict, Code: CodeIdempotencyKeyReused, Message: "Idempotency-Key 已被其他请求使用"}
	case errors.Is(err, idempotencyApp.ErrPending):
		return ErrorMapping{Status: consts.StatusConflict, Code: CodeIdempotencyInProgress, Message: "同一幂等操作仍在执行", RetryAfter: time.Second}
	case errors.Is(err, articleDomain.ErrVersionConflict):
		return ErrorMapping{Status: consts.StatusConflict, Code: CodeArticleVersionConflict, Message: "文章已被其他操作修改，请刷新后重试"}
	case errors.Is(err, articleDomain.ErrAdminOffline):
		return ErrorMapping{Status: consts.StatusForbidden, Code: CodeArticleAdminOffline, Message: "文章已被管理员下架，无法发布"}
	case errors.Is(err, articleDomain.ErrInvalidState):
		return ErrorMapping{Status: consts.StatusConflict, Code: CodeArticleInvalidState, Message: "文章当前状态不允许该操作"}
	case errors.Is(err, articleDomain.ErrForbidden):
		return ErrorMapping{Status: consts.StatusForbidden, Code: CodeForbidden, Message: "无权操作该文章"}
	case errors.Is(err, ports.ErrContentInvalid):
		return ErrorMapping{Status: consts.StatusBadRequest, Code: CodeArticleContentInvalid, Message: err.Error()}
	case errors.Is(err, ports.ErrArticleAssetUnavailable):
		return ErrorMapping{Status: consts.StatusConflict, Code: CodeArticleAssetInvalid, Message: err.Error()}
	case errors.Is(err, articleDomain.ErrNotFound):
		return ErrorMapping{Status: consts.StatusNotFound, Code: CodeArticleNotFound, Message: "文章不存在或不可见"}
	case errors.Is(err, articleDomain.ErrInvalidArgument),
		errors.Is(err, articleDomain.ErrInvalidOrigin),
		errors.Is(err, articleDomain.ErrInvalidRevision),
		errors.Is(err, ErrMalformedBody),
		errors.Is(err, ErrBodyTooLarge):
		return ErrorMapping{Status: consts.StatusBadRequest, Code: CodeValidationFailed, Message: err.Error()}
	case errors.As(err, &rateLimited):
		return ErrorMapping{Status: consts.StatusTooManyRequests, Code: CodeRateLimited,
			Message: "请求过于频繁，请稍后重试", RetryAfter: rateLimited.RetryAfter}
	default:
		return ErrorMapping{Status: consts.StatusServiceUnavailable, Code: CodeDependencyUnavailable, Message: "内容服务暂不可用"}
	}
}

// MapAssetError 映射资产用例错误：存储故障只让资产能力降级，不影响内容主链路。
// 其余错误（头部、幂等、请求正文）沿用内容写入的统一映射。
func MapAssetError(err error) ErrorMapping {
	switch {
	case errors.Is(err, assetApp.ErrQuotaExceeded):
		return ErrorMapping{Status: consts.StatusConflict, Code: CodeAssetQuotaExceeded, Message: assetApp.ErrQuotaExceeded.Error()}
	case errors.Is(err, assetApp.ErrPendingLimitExceeded):
		return ErrorMapping{Status: consts.StatusConflict, Code: CodeAssetPendingLimitExceeded, Message: assetApp.ErrPendingLimitExceeded.Error()}
	case errors.Is(err, ports.ErrAssetStorageUnavailable):
		return ErrorMapping{Status: consts.StatusServiceUnavailable, Code: CodeAssetUnavailable, Message: "图片服务暂不可用", RetryAfter: time.Second}
	case errors.Is(err, assetApp.ErrObjectMissing):
		return ErrorMapping{Status: consts.StatusBadRequest, Code: CodeValidationFailed, Message: "对象尚未上传，请先完成直传"}
	case errors.Is(err, ports.ErrImageUnsupported):
		return ErrorMapping{Status: consts.StatusBadRequest, Code: CodeValidationFailed, Message: "对象不是受支持的 JPEG、PNG 或 WebP"}
	case errors.Is(err, assetDomain.ErrInvalidMedia):
		return ErrorMapping{Status: consts.StatusBadRequest, Code: CodeValidationFailed, Message: assetDomain.ErrInvalidMedia.Error()}
	case errors.Is(err, assetDomain.ErrNotFound), errors.Is(err, assetDomain.ErrForbidden):
		// 非所有者与不存在返回同一结果，不泄露他人资产的存在性。
		return ErrorMapping{Status: consts.StatusNotFound, Code: CodeNotFound, Message: "资产不存在或不可见"}
	case errors.Is(err, assetDomain.ErrInvalidID):
		return ErrorMapping{Status: consts.StatusBadRequest, Code: CodeValidationFailed, Message: assetDomain.ErrInvalidID.Error()}
	default:
		return MapContentError(err)
	}
}

// MapSourceError 映射管理员 Source 管理错误。
// 抓取失败的受控分类不进入响应正文，客户端只需知道是哪一类失败。
func MapSourceError(err error) ErrorMapping {
	switch {
	case errors.Is(err, sourceApp.ErrSourceExists):
		return ErrorMapping{Status: consts.StatusConflict, Code: CodeSourceAlreadyExists, Message: "该 Feed URL 已存在"}
	case errors.Is(err, sourceDomain.ErrVersionConflict):
		return ErrorMapping{Status: consts.StatusConflict, Code: CodeSourceVersionConflict, Message: "来源已被其他操作修改，请刷新后重试"}
	case errors.Is(err, sourceDomain.ErrLeaseHeld):
		return ErrorMapping{Status: consts.StatusConflict, Code: CodeSourceFetchInProgress, Message: "来源正在被其他抓取任务处理", RetryAfter: time.Second}
	case errors.Is(err, sourceDomain.ErrInvalidStatus):
		return ErrorMapping{Status: consts.StatusConflict, Code: CodeValidationFailed, Message: "来源当前状态不允许该操作"}
	case errors.Is(err, sourceDomain.ErrLeaseLost):
		return ErrorMapping{Status: consts.StatusConflict, Code: CodeSourceFetchFailed, Message: "抓取租约已失效，结果未采纳"}
	case errors.Is(err, sourceDomain.ErrNotFound):
		return ErrorMapping{Status: consts.StatusNotFound, Code: CodeNotFound, Message: "来源不存在"}
	case errors.Is(err, sourceDomain.ErrInvalidRunCursor):
		return ErrorMapping{Status: consts.StatusBadRequest, Code: CodeInvalidCursor, Message: "抓取历史游标无效"}
	case errors.Is(err, sourceDomain.ErrInvalidURL),
		errors.Is(err, sourceDomain.ErrInvalidInterval),
		errors.Is(err, sourceDomain.ErrInvalidRun),
		errors.Is(err, sourceDomain.ErrInvalidPageSize),
		errors.Is(err, sourceDomain.ErrInvalidRunState):
		return ErrorMapping{Status: consts.StatusBadRequest, Code: CodeValidationFailed, Message: err.Error()}
	default:
		var failed *sourceApp.FailureError
		if errors.As(err, &failed) {
			// 抓取失败：只回传稳定分类，不回传上游细节。
			return ErrorMapping{Status: consts.StatusConflict, Code: CodeSourceFetchFailed, Message: "抓取失败: " + failed.Code()}
		}
		return MapContentError(err)
	}
}

// WriteMapping 写出映射结果；RetryAfter 大于 0 时附加 Retry-After 头。
func WriteMapping(c *app.RequestContext, mapping ErrorMapping, requestID string) {
	if mapping.RetryAfter > 0 {
		seconds := int(mapping.RetryAfter.Round(time.Second) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		c.Header("Retry-After", strconv.Itoa(seconds))
	}
	WriteError(c, mapping.Status, mapping.Code, mapping.Message, requestID)
}
