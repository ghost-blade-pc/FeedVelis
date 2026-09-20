package account

import (
	"errors"
	"fmt"
	"time"

	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
)

var (
	// ErrRegistrationDisabled 表示注册入口关闭。
	ErrRegistrationDisabled = errors.New("注册入口已关闭")
	// ErrInvalidCredentials 统一表示用户不存在、密码错误或账户禁用，避免泄露账户状态。
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	// ErrAdminAlreadyExists 表示已存在有效管理员，禁止再次初始化。
	ErrAdminAlreadyExists = errors.New("已存在有效管理员")
	// ErrUnauthorized 表示请求缺少可用的当前身份。
	ErrUnauthorized = errors.New("需要登录")
)

// RateLimitedError 表示账号或 IP 维度仍在限制期内；RetryAfter 为剩余等待时间。
// Dimension 用于日志区分触发维度，不影响 HTTP 响应。
type RateLimitedError struct {
	RetryAfter time.Duration
	Dimension  accountDomain.FailureDimension
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("登录尝试过于频繁，请在 %s 后重试", e.RetryAfter.Round(time.Second))
}
