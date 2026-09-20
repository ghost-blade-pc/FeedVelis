package account

import (
	"errors"
	"testing"
	"time"
)

func TestDefaultThrottlePolicy(t *testing.T) {
	policy := DefaultThrottlePolicy()
	if policy.AccountLimit != 5 || policy.IPLimit != 30 {
		t.Fatalf("阈值 = %d/%d", policy.AccountLimit, policy.IPLimit)
	}
	if policy.AccountWindow != 15*time.Minute || policy.IPWindow != 15*time.Minute {
		t.Fatalf("窗口 = %v/%v", policy.AccountWindow, policy.IPWindow)
	}
	if err := policy.Validate(); err != nil {
		t.Fatalf("默认策略应有效: %v", err)
	}
	if got := policy.Limit(DimensionAccount); got != 5 {
		t.Fatalf("账号阈值 = %d", got)
	}
	if got := policy.Limit(DimensionIP); got != 30 {
		t.Fatalf("IP 阈值 = %d", got)
	}
	if got := policy.Window(DimensionIP); got != 15*time.Minute {
		t.Fatalf("IP 窗口 = %v", got)
	}
}

func TestThrottlePolicyValidation(t *testing.T) {
	for _, policy := range []ThrottlePolicy{
		{},
		{AccountWindow: 0, IPWindow: time.Minute, BlockDuration: time.Minute, AccountLimit: 1, IPLimit: 1},
		{AccountWindow: time.Minute, IPWindow: time.Minute, BlockDuration: 0, AccountLimit: 1, IPLimit: 1},
		{AccountWindow: time.Minute, IPWindow: time.Minute, BlockDuration: time.Minute, AccountLimit: 0, IPLimit: 1},
	} {
		if err := policy.Validate(); !errors.Is(err, ErrInvalidThrottlePolicy) {
			t.Fatalf("非法策略应被拒绝: %+v", policy)
		}
	}
}

// BlockedUntil 只按触发时刻计算截止时间；限制期间不延长截止由应用层在已有生效限制时跳过写入保证。
func TestBlockedUntilIsFixedAtFailure(t *testing.T) {
	policy := DefaultThrottlePolicy()
	failedAt := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	until := policy.BlockedUntil(failedAt)
	if got := until.Sub(failedAt); got != 15*time.Minute {
		t.Fatalf("限制时长 = %v", got)
	}
	if !until.After(failedAt) {
		t.Fatal("截止时间必须晚于触发时刻")
	}
}
