package middleware

import (
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	accountApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/account"
	accountDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
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

// AuthenticateOptional 在携带 Bearer 令牌且令牌有效时写入身份，其余情况按匿名继续。
// 用于匿名与作者共用同一路径的读取端点：无令牌或令牌失效都不拒绝，
// 由用例按“公开引用”判定可见性，避免用 401 暴露资源是否存在。
func AuthenticateOptional(resolver IdentityResolver) app.HandlerFunc {
	return func(c context.Context, ctx *app.RequestContext) {
		token := bearerToken(string(ctx.Request.Header.Peek("Authorization")))
		if token != "" {
			if identity, err := resolver.Authenticate(c, token); err == nil {
				ctx.Set(identityKey, identity)
			}
		}
		ctx.Next(c)
	}
}

// RequireAdmin 在已认证身份上追加当前数据库角色检查：普通用户一律拒绝。
// 用例内部仍会依据数据库角色再次校验，本中间件只负责入口收窄。
func RequireAdmin() app.HandlerFunc {
	return func(c context.Context, ctx *app.RequestContext) {
		identity, ok := IdentityFrom(ctx)
		if !ok || identity.User.Role != accountDomain.RoleAdmin {
			presenter.WriteError(ctx, consts.StatusForbidden, presenter.CodeForbidden, "需要管理员权限", RequestIDFrom(ctx))
			ctx.Abort()
			return
		}
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
