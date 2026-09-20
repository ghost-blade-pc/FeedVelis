package middleware

import (
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/presenter"
)

const identityKey = "account_identity"

// IdentityResolver 解析访问令牌对应的当前身份。
type IdentityResolver interface {
	Authenticate(context.Context, string) (accountApp.Identity, error)
}

// Authenticate 解析 Bearer 访问令牌；失败时按统一错误映射拒绝，不放行匿名请求。
func Authenticate(resolver IdentityResolver) app.HandlerFunc {
	return func(c context.Context, ctx *app.RequestContext) {
		token := bearerToken(string(ctx.Request.Header.Peek("Authorization")))
		if token == "" {
			presenter.WriteError(ctx, consts.StatusUnauthorized, presenter.CodeSessionInvalid,
				"缺少访问令牌", RequestIDFrom(ctx))
			ctx.Abort()
			return
		}
		identity, err := resolver.Authenticate(c, token)
		if err != nil {
			presenter.WriteMapping(ctx, presenter.MapAuthError(err), RequestIDFrom(ctx))
			ctx.Abort()
			return
		}
		ctx.Set(identityKey, identity)
		ctx.Next(c)
	}
}

// IdentityFrom 读取由 Authenticate 写入的当前身份。
func IdentityFrom(ctx *app.RequestContext) (accountApp.Identity, bool) {
	value, exists := ctx.Get(identityKey)
	if !exists {
		return accountApp.Identity{}, false
	}
	identity, ok := value.(accountApp.Identity)
	return identity, ok
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}
