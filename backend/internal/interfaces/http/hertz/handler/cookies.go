package handler

import (
	"crypto/rand"
	"encoding/base64"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol"
)

const csrfValueBytes = 32

// CookiePolicy 决定认证 Cookie 的名称与安全属性。
// 生产使用 __Host- 前缀并要求 Secure；本地开发使用无前缀名并允许 Secure=false。
type CookiePolicy struct {
	Secure      bool
	RefreshName string
	CSRFName    string
}

func NewCookiePolicy(secure bool) CookiePolicy {
	if secure {
		return CookiePolicy{Secure: true, RefreshName: "__Host-velis_refresh", CSRFName: "__Host-velis_csrf"}
	}
	return CookiePolicy{Secure: false, RefreshName: "velis_refresh", CSRFName: "velis_csrf"}
}

// SetSessionCookies 写入刷新令牌（HttpOnly）与 CSRF 值（页面可读），Max-Age 不超过会话剩余期限。
func (p CookiePolicy) SetSessionCookies(c *app.RequestContext, refreshToken, csrfValue string, maxAge time.Duration) {
	seconds := int(maxAge / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	c.SetCookie(p.RefreshName, refreshToken, seconds, "/", "", protocol.CookieSameSiteStrictMode, p.Secure, true)
	c.SetCookie(p.CSRFName, csrfValue, seconds, "/", "", protocol.CookieSameSiteStrictMode, p.Secure, false)
}

// ClearSessionCookies 清除两个 Cookie；退出、会话失效与刷新重放时调用。
func (p CookiePolicy) ClearSessionCookies(c *app.RequestContext) {
	c.SetCookie(p.RefreshName, "", -1, "/", "", protocol.CookieSameSiteStrictMode, p.Secure, true)
	c.SetCookie(p.CSRFName, "", -1, "/", "", protocol.CookieSameSiteStrictMode, p.Secure, false)
}

// NewCSRFValue 生成 32 字节随机数的无填充 Base64URL 编码。
func NewCSRFValue() (string, error) {
	value := make([]byte, csrfValueBytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
