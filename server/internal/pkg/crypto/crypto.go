// Package crypto 负责敏感数据的加密与确定性哈希。
//
// 设计要点：
//  1. 加密用 AES-256-GCM（带认证，防篡改），密文格式为 nonce(12) || ciphertext+tag；
//  2. 需要做唯一索引的列（手机号、身份证号）额外存一列 HMAC-SHA256 十六进制串，
//     既保证可查重，又不会因为密文随机化而无法建索引；
//  3. 两把密钥必然不同：密文可解密，HMAC 不可逆，混用会让"密文泄露"直接退化成本人可枚举。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

const (
	// nonceSize 是 GCM 标准 nonce 长度。
	nonceSize = 12
	// tagSize 是 GCM 认证标签长度，用于校验密文最短长度。
	tagSize = 16
	// maxDigits 是随机数字串的长度上限，防止误传大数造成内存分配。
	maxDigits = 12
)

// ErrCipherTooShort 表示密文长度不足以容纳 nonce 与 tag。
var ErrCipherTooShort = errors.New("crypto: 密文长度不足")

// Cipher 是并发安全的加解密器，进程内作为单例注入。
type Cipher struct {
	aead    cipher.AEAD
	hmacKey []byte
}

// NewCipher 用配置里的两把密钥构造实例。
// dataKey 允许任意长度口令（内部 SHA-256 派生 32 字节），hmacKey 会再加固定前缀做域隔离。
func NewCipher(dataKey, hmacKey string) (*Cipher, error) {
	if dataKey == "" {
		return nil, errors.New("crypto: data_key 不能为空")
	}
	if hmacKey == "" {
		return nil, errors.New("crypto: hmac_key 不能为空")
	}

	sum := sha256.Sum256([]byte(dataKey))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, fmt.Errorf("crypto: 初始化 AES 失败: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: 初始化 GCM 失败: %w", err)
	}

	hk := sha256.Sum256([]byte("datechat/hmac/v1/" + hmacKey))
	return &Cipher{aead: aead, hmacKey: hk[:]}, nil
}

// Encrypt 加密明文字符串，返回可直接写入 VARBINARY 列的字节。
// 同一明文每次加密结果都不同（随机 nonce），这是预期行为——需要等值查询请用 HMAC。
func (c *Cipher) Encrypt(plain string) ([]byte, error) {
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("crypto: 生成 nonce 失败: %w", err)
	}

	sealed := c.aead.Seal(nil, nonce, []byte(plain), nil)
	out := make([]byte, 0, nonceSize+len(sealed))
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out, nil
}

// Decrypt 解密 Encrypt 的产物。密文被篡改或密钥不匹配时返回错误。
func (c *Cipher) Decrypt(data []byte) (string, error) {
	if len(data) < nonceSize+tagSize {
		return "", ErrCipherTooShort
	}
	nonce, body := data[:nonceSize], data[nonceSize:]
	plain, err := c.aead.Open(nil, nonce, body, nil)
	if err != nil {
		return "", fmt.Errorf("crypto: 解密失败（密钥不匹配或数据被篡改）: %w", err)
	}
	return string(plain), nil
}

// HMAC 返回 64 位十六进制摘要，用于 phone_hmac / id_card_hmac 等唯一索引列。
func (c *Cipher) HMAC(value string) string {
	mac := hmac.New(sha256.New, c.hmacKey)
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

// Equal 是常量时间字符串比较，用于校验回调签名，避免时序侧信道。
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// RandomDigits 生成 n 位纯数字串（验证码用）。
// 拒绝采样去除取模偏差：只接受 0~249 的随机字节，保证 10 个数字等概率。
func RandomDigits(n int) (string, error) {
	if n <= 0 || n > maxDigits {
		return "", fmt.Errorf("crypto: 随机数字长度需在 1~%d 之间，当前 %d", maxDigits, n)
	}
	out := make([]byte, n)
	var b [1]byte
	for i := 0; i < n; i++ {
		for {
			if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
				return "", fmt.Errorf("crypto: 生成随机数失败: %w", err)
			}
			if b[0] < 250 {
				out[i] = '0' + b[0]%10
				break
			}
		}
	}
	return string(out), nil
}

// RandomToken 生成 n 字节随机数的十六进制串，用于 jti、biz_token 等不透明标识。
func RandomToken(n int) (string, error) {
	if n <= 0 || n > 64 {
		return "", fmt.Errorf("crypto: 随机字节数需在 1~64 之间，当前 %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", fmt.Errorf("crypto: 生成随机数失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
