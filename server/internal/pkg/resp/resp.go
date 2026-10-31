// Package resp 统一 HTTP 响应结构。
// 所有接口（含错误、404、405）都返回 {code,msg,data,trace_id}，客户端只需一套解析逻辑。
package resp

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/datechat/server/internal/pkg/ctxkey"
	"github.com/datechat/server/internal/pkg/errs"
)

// Body 统一响应体。
type Body struct {
	Code    int    `json:"code"`
	Msg     string `json:"msg"`
	Data    any    `json:"data,omitempty"`
	TraceID string `json:"trace_id,omitempty"`
}

// OK 返回成功响应。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Body{
		Code:    int(errs.CodeOK),
		Msg:     "ok",
		Data:    data,
		TraceID: traceID(c),
	})
}

// Created 返回 201，用于资源创建。
func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, Body{
		Code:    int(errs.CodeOK),
		Msg:     "ok",
		Data:    data,
		TraceID: traceID(c),
	})
}

// Fail 返回业务错误。5xx 会被记录到 gin 的错误栈，由访问日志中间件统一落盘。
func Fail(c *gin.Context, err error) {
	e := errs.From(err)
	if e == nil {
		OK(c, nil)
		return
	}
	if e.HTTP >= http.StatusInternalServerError {
		_ = c.Error(err)
	}
	c.AbortWithStatusJSON(e.HTTP, Body{
		Code:    int(e.Code),
		Msg:     e.Msg,
		TraceID: traceID(c),
	})
}

// FailWith 直接用错误码与文案返回，避免调用方临时构造 Error。
func FailWith(c *gin.Context, e *errs.Error) {
	Fail(c, e)
}

func traceID(c *gin.Context) string {
	return c.GetString(ctxkey.TraceID)
}
