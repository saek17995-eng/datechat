package account

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// TokenStore 保存 refresh token 的服务端状态。
//
// 为什么 refresh token 需要服务端状态：
//   - JWT 本身无法吊销，一旦泄露在有效期内一直可用；
//   - 采用"单设备单会话"策略：每个 (uid, deviceID) 只认最新签发的 jti，
//     旧 token 再来换取就判定为重放，直接让该设备重新登录。
type TokenStore interface {
	// SaveRefresh 登录时写入当前 jti。
	SaveRefresh(ctx context.Context, uid int64, deviceID, jti string, ttl time.Duration) error
	// RotateRefresh 原子地"校验旧 jti 并替换为新 jti"。返回 false 表示凭证已失效或被重放。
	RotateRefresh(ctx context.Context, uid int64, deviceID, oldJTI, newJTI string, ttl time.Duration) (bool, error)
	// RevokeRefresh 吊销该设备的登录态（退出登录 / 强制下线）。
	RevokeRefresh(ctx context.Context, uid int64, deviceID string) error
}

// refreshKeyPrefix 用 {} 包住 uid，保证集群模式下同一用户的键落在同一个 slot。
const refreshKeyPrefix = "auth:rt:{%d}:%s"

// rotateScript 是原子轮换脚本。
// KEYS[1]=刷新态键；ARGV[1]=旧 jti；ARGV[2]=新 jti；ARGV[3]=TTL 毫秒。
// 返回 1 表示轮换成功，0 表示当前 jti 不匹配（已轮换过 / 已吊销 / 重放）。
const rotateScript = `
local cur = redis.call('GET', KEYS[1])
if not cur then return 0 end
if cur ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
return 1
`

// RedisTokenStore 基于 Redis 的实现。
type RedisTokenStore struct {
	rdb *redis.Client
}

// NewRedisTokenStore 构造 Redis 版 TokenStore。
func NewRedisTokenStore(rdb *redis.Client) *RedisTokenStore {
	return &RedisTokenStore{rdb: rdb}
}

func refreshKey(uid int64, deviceID string) string {
	return fmt.Sprintf(refreshKeyPrefix, uid, deviceID)
}

// SaveRefresh 实现 TokenStore。
func (s *RedisTokenStore) SaveRefresh(ctx context.Context, uid int64, deviceID, jti string, ttl time.Duration) error {
	if err := s.rdb.Set(ctx, refreshKey(uid, deviceID), jti, ttl).Err(); err != nil {
		return fmt.Errorf("account: 保存刷新令牌状态失败: %w", err)
	}
	return nil
}

// RotateRefresh 实现 TokenStore。
func (s *RedisTokenStore) RotateRefresh(ctx context.Context, uid int64, deviceID, oldJTI, newJTI string, ttl time.Duration) (bool, error) {
	raw, err := s.rdb.Eval(ctx, rotateScript,
		[]string{refreshKey(uid, deviceID)},
		oldJTI, newJTI, ttl.Milliseconds(),
	).Int64()
	if err != nil {
		return false, fmt.Errorf("account: 轮换刷新令牌失败: %w", err)
	}
	return raw == 1, nil
}

// RevokeRefresh 实现 TokenStore。
func (s *RedisTokenStore) RevokeRefresh(ctx context.Context, uid int64, deviceID string) error {
	if err := s.rdb.Del(ctx, refreshKey(uid, deviceID)).Err(); err != nil {
		return fmt.Errorf("account: 吊销刷新令牌失败: %w", err)
	}
	return nil
}
