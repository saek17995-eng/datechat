package crypto

import (
	"bytes"
	"testing"
)

const (
	testDataKey = "unit-test-data-key-0123456789abcdef"
	testHMACKey = "unit-test-hmac-key"
)

func newTestCipher(t *testing.T) *Cipher {
	t.Helper()
	c, err := NewCipher(testDataKey, testHMACKey)
	if err != nil {
		t.Fatalf("构造 Cipher 失败: %v", err)
	}
	return c
}

func TestEncryptDecrypt(t *testing.T) {
	c := newTestCipher(t)

	for _, plain := range []string{"13800138000", "张三", "110101199003071233", ""} {
		enc, err := c.Encrypt(plain)
		if err != nil {
			t.Fatalf("加密 %q 失败: %v", plain, err)
		}
		if plain != "" && bytes.Contains(enc, []byte(plain)) {
			t.Fatalf("密文中不应出现明文 %q", plain)
		}
		got, err := c.Decrypt(enc)
		if err != nil {
			t.Fatalf("解密 %q 失败: %v", plain, err)
		}
		if got != plain {
			t.Fatalf("往返不一致：期望 %q 实际 %q", plain, got)
		}
	}
}

func TestEncryptIsRandomized(t *testing.T) {
	c := newTestCipher(t)

	a, err := c.Encrypt("13800138000")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	b, err := c.Encrypt("13800138000")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("同一明文两次加密结果相同，说明 nonce 没有随机化")
	}
}

func TestDecryptDetectsTamper(t *testing.T) {
	c := newTestCipher(t)

	enc, err := c.Encrypt("13800138000")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	enc[len(enc)-1] ^= 0xff
	if _, err := c.Decrypt(enc); err == nil {
		t.Fatal("密文被篡改后应解密失败")
	}
	if _, err := c.Decrypt([]byte("short")); err == nil {
		t.Fatal("过短的密文应返回错误")
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	c := newTestCipher(t)
	other, err := NewCipher("another-data-key", testHMACKey)
	if err != nil {
		t.Fatalf("构造 Cipher 失败: %v", err)
	}

	enc, err := c.Encrypt("13800138000")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	if _, err := other.Decrypt(enc); err == nil {
		t.Fatal("用错密钥应解密失败")
	}
}

func TestHMACDeterministicAndKeyIsolated(t *testing.T) {
	c := newTestCipher(t)
	other, err := NewCipher(testDataKey, "another-hmac-key")
	if err != nil {
		t.Fatalf("构造 Cipher 失败: %v", err)
	}

	h1 := c.HMAC("13800138000")
	h2 := c.HMAC("13800138000")
	if h1 != h2 {
		t.Fatalf("同一输入 HMAC 应稳定，实际 %s != %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("HMAC 应为 64 位 hex，实际长度 %d", len(h1))
	}
	if c.HMAC("13800138001") == h1 {
		t.Fatal("不同输入不应得到相同 HMAC")
	}
	if other.HMAC("13800138000") == h1 {
		t.Fatal("更换 HMAC 密钥后结果必须变化")
	}
}

func TestRandomDigits(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		code, err := RandomDigits(6)
		if err != nil {
			t.Fatalf("生成验证码失败: %v", err)
		}
		if len(code) != 6 {
			t.Fatalf("验证码长度应为 6，实际 %q", code)
		}
		for _, r := range code {
			if r < '0' || r > '9' {
				t.Fatalf("验证码只能是数字，实际 %q", code)
			}
		}
		seen[code] = true
	}
	if len(seen) < 15 {
		t.Fatalf("20 次生成只有 %d 个不同值，随机性存疑", len(seen))
	}

	if _, err := RandomDigits(0); err == nil {
		t.Fatal("长度 0 应报错")
	}
	if _, err := RandomDigits(20); err == nil {
		t.Fatal("超长应报错")
	}
}

func TestNewCipherRejectsEmptyKey(t *testing.T) {
	if _, err := NewCipher("", testHMACKey); err == nil {
		t.Fatal("空 data_key 应报错")
	}
	if _, err := NewCipher(testDataKey, ""); err == nil {
		t.Fatal("空 hmac_key 应报错")
	}
}
