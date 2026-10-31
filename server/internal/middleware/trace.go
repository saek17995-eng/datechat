package middleware

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/datechat/server/internal/pkg/ctxkey"
)

// HeaderRequestID 客户端可自带链路 ID，便于端到端排查。
const HeaderRequestID = "X-Request-Id"

// traceCtxKey 用私有类型做 context key，避免与其他包的字符串 key 冲突
// （go 官方建议不要用基础类型作为 context key）。
type traceCtxKey struct{}

// Trace 为每个请求生成（或透传）TraceID。
// 同时写入 gin.Context 与 request.Context，方便下游把 ID 带给 RPC / DB 日志。
func Trace() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.GetHeader(HeaderRequestID)
		if traceID == "" || len(traceID) > 64 {
			traceID = uuid.NewString()
		}

		c.Set(ctxkey.TraceID, traceID)
		c.Header(HeaderRequestID, traceID)
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), traceCtxKey{}, traceID))
		c.Next()
	}
}
