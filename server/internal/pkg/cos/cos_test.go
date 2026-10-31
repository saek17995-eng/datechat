package cos

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// TestSignPostObjectReproducible 验证签名与官方算法逐步一致。
// 没有真实 COS 账号可联调，所以用"按文档重算一遍"来锁死实现，防止后续改动悄悄破坏签名。
func TestSignPostObjectReproducible(t *testing.T) {
	cred := Credential{SecretID: "AKIDTEST", SecretKey: "SECRETKEY"}
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

	out, err := SignPostObject(cred, now, PostObjectInput{
		Bucket:   "datechat-1250000000",
		Region:   "ap-guangzhou",
		Key:      "upload/1/avatar/2026/a.jpg",
		MaxBytes: 5 << 20,
		Expire:   30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("签名失败: %v", err)
	}

	if out.Host != "https://datechat-1250000000.cos.ap-guangzhou.myqcloud.com" {
		t.Fatalf("host 不正确: %s", out.Host)
	}
	if out.URL != out.Host+"/upload/1/avatar/2026/a.jpg" {
		t.Fatalf("url 不正确: %s", out.URL)
	}

	keyTime := out.Fields["q-key-time"]
	if keyTime == "" {
		t.Fatal("缺少 q-key-time")
	}
	// 逐步复算
	signKey := hmacSHA1Hex(cred.SecretKey, keyTime)
	want := hmacSHA1Hex(signKey, sha1Hex(out.Policy))
	if out.Fields["q-signature"] != want {
		t.Fatalf("签名不一致\n got=%s\nwant=%s", out.Fields["q-signature"], want)
	}

	// policy 必须包含关键限制条件，否则等于给了客户端一张空白支票
	for _, must := range []string{
		`"bucket":"datechat-1250000000"`,
		`"starts-with","$key","upload/1/avatar/2026/a.jpg"`,
		`"content-length-range",1,5242880`,
		`"q-ak":"AKIDTEST"`,
	} {
		if !strings.Contains(out.Policy, must) {
			t.Fatalf("policy 缺少限制条件 %s：%s", must, out.Policy)
		}
	}

	// 表单字段顺序：签名相关字段必须排在 file 之前，COS 依赖这个顺序校验
	order := out.FieldOrder
	if order[len(order)-1] != "file" {
		t.Fatalf("file 必须是最后一个字段：%v", order)
	}
}

func TestSignPostObjectRejectsBadInput(t *testing.T) {
	cred := Credential{SecretID: "id", SecretKey: "key"}
	now := time.Now()
	if _, err := SignPostObject(Credential{}, now, PostObjectInput{Bucket: "b", Region: "r", Key: "k"}); err == nil {
		t.Fatal("空密钥应报错")
	}
	if _, err := SignPostObject(cred, now, PostObjectInput{Bucket: "b", Region: "r"}); err == nil {
		t.Fatal("缺少 key 应报错")
	}
	if _, err := SignPostObject(cred, now, PostObjectInput{Bucket: "b", Region: "r", Key: "k", ContentType: "imagejpeg"}); err == nil {
		t.Fatal("Content-Type 不是前缀形式应报错")
	}
}

func TestPublicURL(t *testing.T) {
	host := "https://b-1250000000.cos.ap-guangzhou.myqcloud.com"
	if got := PublicURL("", host, "a/b 中.jpg"); got != host+"/a/b%20%E4%B8%AD.jpg" {
		t.Fatalf("路径转义不正确: %s", got)
	}
	if got := PublicURL("https://cdn.example.com", host, "/a.jpg"); got != "https://cdn.example.com/a.jpg" {
		t.Fatalf("CDN 域名未生效: %s", got)
	}
}

func TestHmacSHA1HexKnownVector(t *testing.T) {
	// RFC 2202 风格的固定向量，锁死底层哈希实现
	got := hmacSHA1Hex("key", "The quick brown fox jumps over the lazy dog")
	want := hmacSHA1HexStdlib("key", "The quick brown fox jumps over the lazy dog")
	if got != want {
		t.Fatalf("HMAC 实现不一致: %s vs %s", got, want)
	}
}

func hmacSHA1HexStdlib(key, data string) string {
	m := hmac.New(sha1.New, []byte(key))
	_, _ = m.Write([]byte(data))
	return hex.EncodeToString(m.Sum(nil))
}
