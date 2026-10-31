// Package idgen 生成业务单号。
//
// 单号规则：前缀(1) + 时间(14) + 随机(6)，共 21 位，可读、可排序、索引友好。
// 注意：这不是严格递增序列，若未来需要对账排序请以自增主键或 created_at 为准。
package idgen

import (
	"crypto/rand"
	"math/big"
	"time"
)

const digits = "0123456789"

// WithPrefix 生成 "前缀+时间+随机" 的单号。
func WithPrefix(prefix string) string {
	return prefix + time.Now().Format("20060102150405") + randomDigits(6)
}

// OrderNo 充值订单号。
func OrderNo() string { return WithPrefix("R") }

// CallNo 通话单号。
func CallNo() string { return WithPrefix("C") }

// WithdrawNo 提现单号。
func WithdrawNo() string { return WithPrefix("W") }

func randomDigits(n int) string {
	buf := make([]byte, n)
	for i := 0; i < n; i++ {
		v, err := rand.Int(rand.Reader, big.NewInt(int64(len(digits))))
		if err != nil {
			// crypto/rand 失败属于系统级异常，退化为时间低位，保证可用性优先。
			buf[i] = digits[int(time.Now().UnixNano()>>uint(i*4))%len(digits)]
			continue
		}
		buf[i] = digits[v.Int64()]
	}
	return string(buf)
}
