// Package health 提供存活、就绪与连通性探测接口。
package health

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/datechat/server/internal/config"
	"github.com/datechat/server/internal/pkg/clock"
	"github.com/datechat/server/internal/pkg/ctxkey"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/metrics"
	"github.com/datechat/server/internal/pkg/resp"
)

// Liveness 只证明进程还在跑，不依赖任何外部组件。
// K8s 的 livenessProbe 用它：数据库抖动不应该导致容器被重启。
func Liveness(c *gin.Context) {
	resp.OK(c, gin.H{
		"status": "alive",
		"time":   clock.Now().Format(time.RFC3339),
	})
}

// Readiness 检查 MySQL 与 Redis。
// 任一不可用返回 503，让负载均衡把流量摘走。
func Readiness(db *gorm.DB, rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		detail := gin.H{"mysql": "ok", "redis": "ok"}
		healthy := true

		if sqlDB, err := db.DB(); err != nil {
			detail["mysql"] = "error: " + err.Error()
			healthy = false
		} else if err := sqlDB.PingContext(ctx); err != nil {
			detail["mysql"] = "error: " + err.Error()
			healthy = false
		}

		if err := rdb.Ping(ctx).Err(); err != nil {
			detail["redis"] = "error: " + err.Error()
			healthy = false
		}

		if !healthy {
			c.JSON(http.StatusServiceUnavailable, resp.Body{
				Code:    int(errs.CodeServiceBusy),
				Msg:     "依赖不可用",
				Data:    detail,
				TraceID: c.GetString(ctxkey.TraceID),
			})
			return
		}

		resp.OK(c, detail)
	}
}

// Ping 返回服务身份信息，便于确认版本与部署环境。
func Ping(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		resp.OK(c, gin.H{
			"name":        cfg.App.Name,
			"version":     cfg.App.Version,
			"env":         cfg.App.Env,
			"server_time": clock.Now().Format(time.RFC3339),
		})
	}
}

// Metrics 暴露 Prometheus 文本指标。
//
// 不挂 JWT：Prometheus 抓取器不会带业务令牌。安全由两件事保证：
//  1. 配了 observe.metrics_token 时必须携带（?token= 或 X-Metrics-Token）；
//  2. 生产环境不要把它暴露到公网——建议只在内网/VPC 里开放这个路径。
func Metrics(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token != "" {
			got := c.Query("token")
			if got == "" {
				got = c.GetHeader("X-Metrics-Token")
			}
			if got != token {
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}
		}
		c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		c.String(http.StatusOK, metrics.RenderText())
	}
}
