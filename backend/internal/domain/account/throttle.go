package account

import (
	"errors"
	"time"
)

// FailureDimension 是登录失败计数维度：账号与 IP 分开计数、任一命中即拒绝。
type FailureDimension string

const (
	DimensionAccount FailureDimension = "account"
	DimensionIP      FailureDimension = "ip"
)

var ErrInvalidThrottlePolicy = errors.New("登录限流参数无效")

// ThrottlePolicy 是登录失败限流参数，计数采用滚动窗口。
type ThrottlePolicy struct {
	AccountWindow time.Duration
	AccountLimit  int
	IPWindow      time.Duration
	IPLimit       int
	BlockDuration time.Duration
}

// DefaultThrottlePolicy 返回规格默认值：账号 15 分钟 5 次、IP 15 分钟 30 次，任一中止 15 分钟。
func DefaultThrottlePolicy() ThrottlePolicy {
	return ThrottlePolicy{
		AccountWindow: 15 * time.Minute,
		AccountLimit:  5,
		IPWindow:      15 * time.Minute,
		IPLimit:       30,
		BlockDuration: 15 * time.Minute,
	}
}

func (p ThrottlePolicy) Validate() error {
	if p.AccountWindow <= 0 || p.IPWindow <= 0 || p.BlockDuration <= 0 || p.AccountLimit <= 0 || p.IPLimit <= 0 {
		return ErrInvalidThrottlePolicy
	}
	return nil
}

// Window 返回维度的滚动统计窗口。
func (p ThrottlePolicy) Window(dim FailureDimension) time.Duration {
	if dim == DimensionIP {
		return p.IPWindow
	}
	return p.AccountWindow
}

// Limit 返回维度触发限制所需的失败次数。
func (p ThrottlePolicy) Limit(dim FailureDimension) int {
	if dim == DimensionIP {
		return p.IPLimit
	}
	return p.AccountLimit
}

// BlockedUntil 返回达到阈值后的限制截止时间；限制期间不延长该时间。
func (p ThrottlePolicy) BlockedUntil(failedAt time.Time) time.Time {
	return failedAt.Add(p.BlockDuration)
}
