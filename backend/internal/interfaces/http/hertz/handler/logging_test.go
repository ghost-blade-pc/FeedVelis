package handler

import (
	"errors"
	"reflect"
	"testing"
	"time"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/presenter"
)

func TestLogAttributesDistinguishesThrottleDimension(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want []any
	}{
		{"账号维度限流", &accountApp.RateLimitedError{RetryAfter: time.Minute, Dimension: accountDomain.DimensionAccount}, []any{"dimension", "account"}},
		{"IP 维度限流", &accountApp.RateLimitedError{RetryAfter: time.Minute, Dimension: accountDomain.DimensionIP}, []any{"dimension", "ip"}},
		{"维度未知时不补字段", &accountApp.RateLimitedError{RetryAfter: time.Minute}, nil},
		{"刷新重放", accountDomain.ErrRefreshReplay, []any{"event", "refresh_replay"}},
		{"包装后的刷新重放仍可识别", errors.Join(accountDomain.ErrRefreshReplay), []any{"event", "refresh_replay"}},
		{"一般刷新失败不补字段", accountDomain.ErrRefreshUnknown, nil},
		{"凭证错误不补字段", accountApp.ErrInvalidCredentials, nil},
	}
	for _, c := range cases {
		if got := logAttributes(c.err); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%s：期望 %v，实际 %v", c.name, c.want, got)
		}
	}
}

// 日志字段不得改变响应语义：重放仍与一般刷新失败同码，限流维度不影响状态码。
func TestLogAttributesDoNotChangeResponseMapping(t *testing.T) {
	replay := presenter.MapAuthError(accountDomain.ErrRefreshReplay)
	if replay.Status != 401 || replay.Code != "AUTH_SESSION_INVALID" {
		t.Fatalf("重放响应应为 401 AUTH_SESSION_INVALID，实际 %d %s", replay.Status, replay.Code)
	}
	limited := presenter.MapAuthError(&accountApp.RateLimitedError{RetryAfter: time.Minute, Dimension: accountDomain.DimensionIP})
	if limited.Status != 429 || limited.Code != "AUTH_RATE_LIMITED" {
		t.Fatalf("限流响应应为 429 AUTH_RATE_LIMITED，实际 %d %s", limited.Status, limited.Code)
	}
}
