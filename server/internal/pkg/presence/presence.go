// Package presence 在线状态的唯一约定。
//
// 在线只存在 Redis 里：它是高频、可丢失的数据，落库只会给 MySQL 加压。
// 心跳写入方（资料模块）与读取方（附近的人）必须共用同一套 key 与 TTL，
// 否则会出现"心跳写了 A 键、列表读 B 键"，表现为所有人在列表里都是离线。
package presence

import "strconv"

// KeyPrefix 在线键前缀。
const KeyPrefix = "presence:user:"

// DefaultTTLSec 默认在线 TTL。必须大于心跳间隔的 2 倍，否则弱网下会闪烁。
const DefaultTTLSec = 120

// Key 生成在线键。
func Key(uid int64) string { return KeyPrefix + strconv.FormatInt(uid, 10) }
