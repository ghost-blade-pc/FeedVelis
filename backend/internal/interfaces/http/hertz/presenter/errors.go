package presenter

import (
	"errors"
	"strconv"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
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
