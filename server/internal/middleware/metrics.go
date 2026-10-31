package middleware

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/datechat/server/internal/pkg/metrics"
)

// 延迟分桶：接口延迟只需要区分"正常 / 变慢 / 很慢 / 超时边缘"，
// 桶太多只会让指标体积膨胀，对告警没有额外帮助。
var latencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10}

// Metrics 请求打点：QPS、延迟直方图、状态码分布。
//
// 用路由模板（/v1/users/:uid）而不是真实路径做标签：
// 真实路径带 UID，基数无限大，会把 Prometheus 的时序打爆。
func Metrics() gin.HandlerFunc {
	hist := metrics.MustHistogram("http_request_duration_seconds", latencyBuckets)
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		status := strconv.Itoa(c.Writer.Status())
		labels := []string{
			"route=\"" + route + "\"",
			"method=\"" + c.Request.Method + "\"",
			"status=\"" + status + "\"",
		}
		metrics.Inc("http_requests_total", labels...)
		hist.Observe(time.Since(start).Seconds())
	}
}

// SlowRequest 慢请求告警日志。
//
// 阈值以上的请求单独打一条 warn：访问日志是 info 级别，
// 混在里面根本没人看得见，而慢请求往往是数据库缺索引的第一个信号。
func SlowRequest(log *zap.Logger, threshold time.Duration) gin.HandlerFunc {
	if threshold <= 0 {
		threshold = time.Second
	}
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		cost := time.Since(start)
		if cost < threshold {
			return
		}
		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		log.Warn("慢请求",
			zap.String("route", route),
			zap.String("method", c.Request.Method),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("cost", cost),
			zap.String("trace_id", c.GetString("trace_id")),
		)
		metrics.Inc("http_slow_requests_total", "route=\""+route+"\"")
	}
}
