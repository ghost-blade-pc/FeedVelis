package handler

import (
	"errors"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
)

// logAttributes 补充分布式计数需要的低基数日志字段，且只影响日志：
// 限流记录触发维度；刷新重放与一般刷新失败共享同一 HTTP 错误码，因此单独标记事件名。
// 返回值为 slog 的键值对序列，字段名与既有认证日志保持一致。
func logAttributes(err error) []any {
	var rateLimited *accountApp.RateLimitedError
	switch {
	case errors.As(err, &rateLimited) && rateLimited.Dimension != "":
		return []any{"dimension", string(rateLimited.Dimension)}
	case errors.Is(err, accountDomain.ErrRefreshReplay):
		return []any{"event", "refresh_replay"}
	}
	return nil
}
