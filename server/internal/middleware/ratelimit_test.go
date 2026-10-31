package middleware

import (
	"testing"
	"time"
)

func TestRateLimiterAllowsWithinBurst(t *testing.T) {
	limiter := NewRateLimiter(1, 2, time.Minute)

	if !limiter.Allow("1.1.1.1") {
		t.Fatal("令牌桶初始应有 2 个令牌，第一次应放行")
	}
	if !limiter.Allow("1.1.1.1") {
		t.Fatal("第二个令牌也应放行")
	}
	if limiter.Allow("1.1.1.1") {
		t.Fatal("令牌耗尽后应拒绝")
	}
}

func TestRateLimiterIsolatesByKey(t *testing.T) {
	limiter := NewRateLimiter(1, 1, time.Minute)

	if !limiter.Allow("1.1.1.1") {
		t.Fatal("IP A 首次应放行")
	}
	if limiter.Allow("1.1.1.1") {
		t.Fatal("IP A 第二次应被拒")
	}
	if !limiter.Allow("2.2.2.2") {
		t.Fatal("不同 IP 应独立计数，B 首次必须放行")
	}
}

func TestRateLimiterRefillsOverTime(t *testing.T) {
	// 10 rps 的桶，burst 1：等 150ms 应该回填至少 1 个令牌。
	limiter := NewRateLimiter(10, 1, time.Minute)

	if !limiter.Allow("3.3.3.3") {
		t.Fatal("首次应放行")
	}
	if limiter.Allow("3.3.3.3") {
		t.Fatal("立即再次请求应被拒")
	}

	time.Sleep(150 * time.Millisecond)
	if !limiter.Allow("3.3.3.3") {
		t.Fatal("等待后令牌应回填")
	}
}
