// Package phone 负责中国大陆手机号的归一化、校验与脱敏。
// 归一化必须先于任何存储与比较：客户端可能传 "+86 138-0013-8000"。
package phone

import (
	"errors"
	"strings"
)

// ErrInvalid 表示不是合法的中国大陆手机号。
var ErrInvalid = errors.New("手机号格式不正确")

// replacer 去掉用户习惯性输入的分隔符。
var replacer = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", "\u00a0", "")

// Normalize 把各种写法统一成 11 位纯数字。
func Normalize(raw string) (string, error) {
	s := replacer.Replace(strings.TrimSpace(raw))
	if strings.HasPrefix(s, "+86") {
		s = s[len("+86"):]
	} else if strings.HasPrefix(s, "0086") {
		s = s[len("0086"):]
	} else if strings.HasPrefix(s, "86") && len(s) == 13 {
		s = s[2:]
	}
	if len(s) != 11 || s[0] != '1' {
		return "", ErrInvalid
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return "", ErrInvalid
		}
	}
	// 第二位为 3~9 才是现网号段；1 和 2 是预留/物联网号段，直接拒。
	if s[1] < '3' || s[1] > '9' {
		return "", ErrInvalid
	}
	return s, nil
}

// Mask 返回展示用脱敏形式：138****8000。
func Mask(s string) string {
	if len(s) != 11 {
		return "***"
	}
	return s[:3] + "****" + s[7:]
}
