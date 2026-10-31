package middleware

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/datechat/server/internal/pkg/clock"
	"github.com/datechat/server/internal/pkg/ctxkey"
)

// AccessLog 结构化访问日志。
// 4xx 记 warn、5xx 记 error，方便直接按级别做告警。
func AccessLog(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := clock.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("query", query),
			zap.Int("status", c.Writer.Status()),
			zap.Int("size", c.Writer.Size()),
			zap.Duration("cost", clock.Since(start)),
			zap.String("ip", c.ClientIP()),
			zap.String("trace_id", c.GetString(ctxkey.TraceID)),
		}
		if uid := CurrentUID(c); uid > 0 {
			fields = append(fields, zap.Int64("uid", uid))
		}
		if len(c.Errors) > 0 {
			fields = append(fields, zap.String("errors", c.Errors.String()))
		}

		switch {
		case c.Writer.Status() >= 500:
			log.Error("access", fields...)
		case c.Writer.Status() >= 400:
			log.Warn("access", fields...)
		default:
			log.Info("access", fields...)
		}
	}
}
