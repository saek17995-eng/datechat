package bootstrap

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/datechat/server/internal/config"
)

// NewRedis 建立单实例客户端并等待可用。
// 集群模式后续用 redis.NewClusterClient 替换，业务侧只依赖 redis.UniversalClient 接口。
func NewRedis(cfg config.RedisConfig, log *zap.Logger) (*redis.Client, error) {
	if len(cfg.Addrs) == 0 {
		return nil, fmt.Errorf("redis.addrs 为空")
	}

	client := redis.NewClient(&redis.Options{
		Addr:         cfg.Addrs[0],
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		DialTimeout:  cfg.DialTimeout.D(),
		ReadTimeout:  cfg.ReadTimeout.D(),
		WriteTimeout: cfg.WriteTimeout.D(),
	})

	err := waitFor("redis", 15, 2*time.Second, log, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return client.Ping(ctx).Err()
	})
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("初始化 Redis 失败: %w", err)
	}

	log.Info("Redis 已连接", zap.String("addr", cfg.Addrs[0]), zap.Int("db", cfg.DB))
	return client, nil
}
