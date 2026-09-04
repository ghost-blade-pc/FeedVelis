// Package middleware 提供 Hertz HTTP 中间件。
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"

	"github.com/cloudwego/hertz/pkg/app"
)

const (
	RequestIDHeader = "X-Request-ID"
	requestIDKey    = "request_id"
)

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

func RequestID(c context.Context, ctx *app.RequestContext) {
	id := string(ctx.Request.Header.Peek(RequestIDHeader))
	if !validRequestID.MatchString(id) {
		id = newRequestID()
	}
	ctx.Set(requestIDKey, id)
	ctx.Header(RequestIDHeader, id)
	ctx.Next(c)
}

func RequestIDFrom(ctx *app.RequestContext) string {
	return ctx.GetString(requestIDKey)
}

func newRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("生成 request ID 失败: " + err.Error())
	}
	return hex.EncodeToString(value[:])
}
