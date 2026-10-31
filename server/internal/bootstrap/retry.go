package bootstrap

import (
	"fmt"
	"time"

	"go.uber.org/zap"
)

// waitFor 轮询等待依赖就绪。
// 容器化部署时 MySQL/Redis 往往比应用晚启动 5-30 秒，没有这个重试就会出现启动即崩。
func waitFor(name string, attempts int, interval time.Duration, log *zap.Logger, fn func() error) error {
	var lastErr error
	for i := 1; i <= attempts; i++ {
		if err := fn(); err == nil {
			if i > 1 {
				log.Info("依赖已就绪", zap.String("dependency", name), zap.Int("attempt", i))
			}
			return nil
		} else {
			lastErr = err
			log.Warn("依赖尚未就绪，稍后重试",
				zap.String("dependency", name),
				zap.Int("attempt", i),
				zap.Int("max_attempts", attempts),
				zap.Error(err),
			)
		}
		if i < attempts {
			time.Sleep(interval)
		}
	}
	return fmt.Errorf("%s 重试 %d 次后仍不可用: %w", name, attempts, lastErr)
}
