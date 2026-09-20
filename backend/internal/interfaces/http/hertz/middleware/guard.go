package middleware

import (
	"context"
	"crypto/subtle"
	"net/url"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/presenter"
)

// GuardPolicy 是来源与 CSRF 校验所需的端点策略。
type GuardPolicy struct {
	// AllowedOrigin 是精确同源来源，例如 https://velis.example.com。
	AllowedOrigin string
	// CSRFName 是 CSRF Cookie 名；为空时使用开发环境默认值。
	CSRFName string
}

// RequireJSON 只接受 application/json 正文。
func RequireJSON() app.HandlerFunc {
	return func(c context.Context, ctx *app.RequestContext) {
		contentType := strings.ToLower(string(ctx.Request.Header.ContentType()))
		if !strings.HasPrefix(contentType, "application/json") {
			rejectGuard(ctx, consts.StatusBadRequest, presenter.CodeValidationFailed, "请求正文必须是 application/json")
			return
		}
		ctx.Next(c)
	}
}

// RequireTrustedOrigin 校验精确来源：优先 Origin，缺失时用精确 Referer 回退，两者都缺即拒绝；
// 并拒绝 Sec-Fetch-Site: cross-site。same-site 不能替代精确同源判断。
func RequireTrustedOrigin(policy GuardPolicy) app.HandlerFunc {
	return func(c context.Context, ctx *app.RequestContext) {
		if string(ctx.Request.Header.Peek("Sec-Fetch-Site")) == "cross-site" {
			rejectGuard(ctx, consts.StatusForbidden, presenter.CodeCSRFRejected, "来源校验失败")
			return
		}
		origin := string(ctx.Request.Header.Peek("Origin"))
		if origin == "" {
			parsed, err := url.Parse(string(ctx.Request.Header.Peek("Referer")))
			if err != nil || parsed.Scheme == "" || parsed.Host == "" {
				rejectGuard(ctx, consts.StatusForbidden, presenter.CodeCSRFRejected, "来源校验失败")
				return
			}
			origin = parsed.Scheme + "://" + parsed.Host
		}
		if !strings.EqualFold(strings.TrimSuffix(origin, "/"), strings.TrimSuffix(policy.AllowedOrigin, "/")) {
			rejectGuard(ctx, consts.StatusForbidden, presenter.CodeCSRFRejected, "来源校验失败")
			return
		}
		ctx.Next(c)
	}
}

// RequireCSRF 要求 X-CSRF-Token 与 CSRF Cookie 常量时间相等。
func RequireCSRF(policy GuardPolicy) app.HandlerFunc {
	return func(c context.Context, ctx *app.RequestContext) {
		header := ctx.Request.Header.Peek("X-CSRF-Token")
		cookie := ctx.Cookie(policy.CSRFName)
		if len(header) == 0 || len(cookie) == 0 || subtle.ConstantTimeCompare(header, cookie) != 1 {
			rejectGuard(ctx, consts.StatusForbidden, presenter.CodeCSRFRejected, "CSRF 校验失败")
			return
		}
		ctx.Next(c)
	}
}

func rejectGuard(ctx *app.RequestContext, status int, code, message string) {
	presenter.WriteError(ctx, status, code, message, RequestIDFrom(ctx))
	ctx.Abort()
}
