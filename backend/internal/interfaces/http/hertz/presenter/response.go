// Package presenter 将 Application 结果和错误映射为 HTTP 响应。
package presenter

import (
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func WriteError(c *app.RequestContext, status int, code, message, requestID string) {
	c.JSON(status, ErrorBody{Error: ErrorDetail{
		Code:      code,
		Message:   message,
		RequestID: requestID,
	}})
}

func WriteNotFound(c *app.RequestContext, requestID string) {
	WriteError(c, consts.StatusNotFound, "NOT_FOUND", "请求的资源不存在", requestID)
}
