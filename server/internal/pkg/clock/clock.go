// Package clock 提供可替换的时钟。
//
// 计费逻辑强依赖时间（通话时长、T+7 解冻、订单超时），直接调 time.Now 会让测试无法稳定。
// 业务代码统一走 clock.Now()，测试用 SetForTest 注入假时钟。
package clock

import (
	"sync"
	"time"
)

var (
	mu      sync.RWMutex
	nowFunc = time.Now
)

// Now 返回当前时间。
func Now() time.Time {
	mu.RLock()
	defer mu.RUnlock()
	return nowFunc()
}

// Since 返回距 t 的时长。
func Since(t time.Time) time.Duration { return Now().Sub(t) }

// SetForTest 仅在测试中使用，返回的函数用于恢复真实时钟。
func SetForTest(f func() time.Time) (restore func()) {
	mu.Lock()
	previous := nowFunc
	nowFunc = f
	mu.Unlock()

	return func() {
		mu.Lock()
		nowFunc = previous
		mu.Unlock()
	}
}
