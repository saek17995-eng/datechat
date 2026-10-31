package middleware

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/resp"
)

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter 按客户端 IP 做令牌桶限流。
//
// 说明：这是单机内存实现，多实例部署时各实例独立计数，真实 QPS 上限 = rps × 实例数。
// 上线后应换成 Redis 滑动窗口（同一份 key 计数），P1 接入。
type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rps      rate.Limit
	burst    int
	ttl      time.Duration
}

// NewRateLimiter 创建限流器并启动过期清理协程（随进程生命周期存在）。
func NewRateLimiter(rps float64, burst int, ttl time.Duration) *RateLimiter {
	if rps <= 0 {
		rps = 50
	}
	if burst <= 0 {
		burst = 100
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}

	l := &RateLimiter{
		visitors: make(map[string]*visitor),
		rps:      rate.Limit(rps),
		burst:    burst,
		ttl:      ttl,
	}
	go l.cleanupLoop()
	return l
}

// Allow 判断该 key 是否放行。
func (l *RateLimiter) Allow(key string) bool {
	l.mu.Lock()
	v, ok := l.visitors[key]
	if !ok {
		v = &visitor{limiter: rate.NewLimiter(l.rps, l.burst)}
		l.visitors[key] = v
	}
	v.lastSeen = time.Now()
	l.mu.Unlock()

	return v.limiter.Allow()
}

func (l *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(l.ttl)
	defer ticker.Stop()

	for range ticker.C {
		deadline := time.Now().Add(-l.ttl)

		l.mu.Lock()
		for key, v := range l.visitors {
			if v.lastSeen.Before(deadline) {
				delete(l.visitors, key)
			}
		}
		l.mu.Unlock()
	}
}

// RateLimit 暴露为 gin 中间件。
func RateLimit(rps float64, burst int, ttl time.Duration) gin.HandlerFunc {
	limiter := NewRateLimiter(rps, burst, ttl)

	return func(c *gin.Context) {
		if !limiter.Allow(c.ClientIP()) {
			resp.Fail(c, errs.ErrTooManyRequests)
			return
		}
		c.Next()
	}
}
