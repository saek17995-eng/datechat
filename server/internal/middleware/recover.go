package middleware

import (
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/datechat/server/internal/pkg/ctxkey"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/resp"
)

// Recover 兜住 panic，避免单个请求打挂整个进程。
// 堆栈只进日志，响应体只给通用文案。
func Recover(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// http.ErrAbortHandler 是标准库用于主动中断连接的特殊值，必须继续向上抛。
			if rec == http.ErrAbortHandler {
				panic(rec)
			}

			log.Error("panic recovered",
				zap.Any("panic", rec),
				zap.String("method", c.Request.Method),
				zap.String("path", c.Request.URL.Path),
				zap.String("trace_id", c.GetString(ctxkey.TraceID)),
				zap.Int64("uid", CurrentUID(c)),
				zap.ByteString("stack", debug.Stack()),
			)
			resp.Fail(c, errs.ErrInternal)
		}()

		c.Next()
	}
}
