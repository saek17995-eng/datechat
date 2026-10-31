// Package idcard 提供中国大陆 18 位居民身份证号的校验、解析与脱敏。
//
// 只做格式与校验位校验，不做"真人是否存在"的判断——那必须由实名核身通道完成。
package idcard

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// 校验位权重与映射表，依据 GB 11643-1999。
var (
	weights   = [17]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	checkCode = [11]byte{'1', '0', 'X', '9', '8', '7', '6', '5', '4', '3', '2'}
)

// 省份代码前两位白名单。
var provinceCodes = map[string]bool{
	"11": true, "12": true, "13": true, "14": true, "15": true,
	"21": true, "22": true, "23": true,
	"31": true, "32": true, "33": true, "34": true, "35": true, "36": true, "37": true,
	"41": true, "42": true, "43": true, "44": true, "45": true, "46": true,
	"50": true, "51": true, "52": true, "53": true, "54": true,
	"61": true, "62": true, "63": true, "64": true, "65": true,
	"71": true, "81": true, "82": true, "83": true, "91": true,
}

// ErrInvalid 表示身份证号不合法。错误文案可直接透给用户。
var ErrInvalid = errors.New("身份证号格式不正确")

// ErrInvalidChecksum 表示校验位不匹配，单独区分便于给用户更精确的提示。
var ErrInvalidChecksum = errors.New("身份证号校验位不正确，请核对后重试")

// Normalize 去掉空格并把末位 x 统一成大写。
func Normalize(raw string) (string, error) {
	s := strings.ToUpper(strings.TrimSpace(strings.ReplaceAll(raw, " ", "")))
	if len(s) == 15 {
		return "", errors.New("暂不支持 15 位旧版身份证，请使用 18 位号码")
	}
	if len(s) != 18 {
		return "", ErrInvalid
	}
	for i := 0; i < 17; i++ {
		if s[i] < '0' || s[i] > '9' {
			return "", ErrInvalid
		}
	}
	last := s[17]
	if (last < '0' || last > '9') && last != 'X' {
		return "", ErrInvalid
	}
	if !provinceCodes[s[:2]] {
		return "", ErrInvalid
	}
	if _, err := BirthDate(s); err != nil {
		return "", ErrInvalid
	}
	return s, nil
}

// Validate 校验格式并核对校验位。
func Validate(raw string) error {
	s, err := Normalize(raw)
	if err != nil {
		return err
	}
	sum := 0
	for i := 0; i < 17; i++ {
		sum += int(s[i]-'0') * weights[i]
	}
	if checkCode[sum%11] != s[17] {
		return ErrInvalidChecksum
	}
	return nil
}

// BirthDate 解析出生日期（第 7~14 位）。
func BirthDate(raw string) (time.Time, error) {
	if len(raw) != 18 {
		return time.Time{}, ErrInvalid
	}
	born, err := time.ParseInLocation("20060102", raw[6:14], time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("身份证号中的出生日期不合法: %w", err)
	}
	now := time.Now()
	if born.After(now) {
		return time.Time{}, errors.New("身份证号中的出生日期晚于当前时间")
	}
	if born.Year() < 1900 {
		return time.Time{}, errors.New("身份证号中的出生日期早于 1900 年")
	}
	return born, nil
}

// Age 计算周岁年龄，用于附近人筛选与未成年人保护。
func Age(raw string, now time.Time) (int, error) {
	born, err := BirthDate(raw)
	if err != nil {
		return 0, err
	}
	age := now.Year() - born.Year()
	if now.Month() < born.Month() || (now.Month() == born.Month() && now.Day() < born.Day()) {
		age--
	}
	if age < 0 {
		return 0, errors.New("年龄计算异常")
	}
	return age, nil
}

// Gender 按第 17 位奇偶判断：奇数为男(1)，偶数为女(2)。
func Gender(raw string) int {
	if len(raw) != 18 {
		return 0
	}
	if (raw[16]-'0')%2 == 1 {
		return 1
	}
	return 2
}

// Mask 返回脱敏形式：110101********1234。
func Mask(raw string) string {
	if len(raw) != 18 {
		return "***"
	}
	return raw[:6] + "********" + raw[14:]
}
