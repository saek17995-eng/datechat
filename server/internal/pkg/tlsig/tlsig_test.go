package tlsig

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"io"
	"testing"
	"time"
)

const (
	testSDKAppID = 1400000000
	testKey      = "unit-test-im-secret-key"
)

func newGen(t *testing.T) *Generator {
	t.Helper()
	g, err := New(testSDKAppID, testKey, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("构造生成器失败: %v", err)
	}
	return g
}

// decode 解开 UserSig，返回压缩前的 JSON 文档。
func decode(t *testing.T, sig string) Doc {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		t.Fatalf("UserSig 不是合法 base64: %v", err)
	}
	zr, err := zlib.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("UserSig 解压失败: %v", err)
	}
	defer func() { _ = zr.Close() }()

	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("读取解压内容失败: %v", err)
	}
	var doc Doc
	if err := json.Unmarshal(plain, &doc); err != nil {
		t.Fatalf("UserSig 内容不是合法 JSON: %v (%s)", err, plain)
	}
	return doc
}

func TestGenerateStructure(t *testing.T) {
	g := newGen(t)
	fixed := time.Unix(1_800_000_000, 0)

	sig, expireAt, err := g.GenerateAt("u_1001", fixed)
	if err != nil {
		t.Fatalf("生成 UserSig 失败: %v", err)
	}
	if expireAt.Sub(fixed) != 7*24*time.Hour {
		t.Fatalf("过期时间应为签发时间 +7 天，实际 %s", expireAt.Sub(fixed))
	}

	doc := decode(t, sig)
	if doc.Ver != "2.0" {
		t.Fatalf("TLS.ver 期望 2.0，实际 %s", doc.Ver)
	}
	if doc.Identifier != "u_1001" {
		t.Fatalf("TLS.identifier 期望 u_1001，实际 %s", doc.Identifier)
	}
	if doc.SdkAppID != testSDKAppID {
		t.Fatalf("TLS.sdkappid 期望 %d，实际 %d", testSDKAppID, doc.SdkAppID)
	}
	if doc.Time != fixed.Unix() {
		t.Fatalf("TLS.time 期望 %d，实际 %d", fixed.Unix(), doc.Time)
	}
	if doc.Expire != int64((7 * 24 * time.Hour).Seconds()) {
		t.Fatalf("TLS.expire 期望 %d，实际 %d", int64((7 * 24 * time.Hour).Seconds()), doc.Expire)
	}

	// 签名必须能被同密钥复算出来，证明签名覆盖的原文与文档字段一致。
	want := sign(testKey, doc.Identifier, doc.SdkAppID, doc.Time, doc.Expire)
	if doc.Sig != want {
		t.Fatalf("TLS.sig 复算不一致：期望 %s 实际 %s", want, doc.Sig)
	}
}

func TestGenerateIsDeterministicForSameTime(t *testing.T) {
	g := newGen(t)
	fixed := time.Unix(1_800_000_000, 0)

	a, _, err := g.GenerateAt("u_1001", fixed)
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	b, _, err := g.GenerateAt("u_1001", fixed)
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	// JSON 由固定结构体序列化，字段顺序稳定，同输入必须同输出。
	if a != b {
		t.Fatal("相同输入应生成相同 UserSig（便于排障比对）")
	}
}

func TestVerifyRoundTrip(t *testing.T) {
	g := newGen(t)

	sig, _, err := g.Generate("u_2002")
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	doc, err := g.Verify(sig)
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if doc.Identifier != "u_2002" {
		t.Fatalf("identifier 期望 u_2002，实际 %s", doc.Identifier)
	}
}

func TestVerifyRejectsTampered(t *testing.T) {
	g := newGen(t)
	sig, _, err := g.Generate("u_3003")
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}

	if _, err := g.Verify("not-base64!!"); err == nil {
		t.Fatal("非 base64 内容应校验失败")
	}

	// 用另一个密钥签发的 UserSig 必须被拒。
	other, err := New(testSDKAppID, "another-secret", time.Hour)
	if err != nil {
		t.Fatalf("构造生成器失败: %v", err)
	}
	foreign, _, err := other.Generate("u_3003")
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if _, err := g.Verify(foreign); err == nil {
		t.Fatal("换密钥签发的 UserSig 应校验失败")
	}
	_ = sig
}

func TestVerifyRejectsExpired(t *testing.T) {
	g := newGen(t)
	sig, _, err := g.GenerateAt("u_4004", time.Now().Add(-8*24*time.Hour))
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if _, err := g.Verify(sig); err == nil {
		t.Fatal("已过期的 UserSig 应校验失败")
	}
}

func TestSignContentFormat(t *testing.T) {
	got := SignContent("u_1", 1400000000, 1800000000, 604800)
	want := "TLS.identifier:u_1\nTLS.sdkappid:1400000000\nTLS.time:1800000000\nTLS.expire:604800\n"
	if got != want {
		t.Fatalf("签名原文格式不符：\n期望 %q\n实际 %q", want, got)
	}
}

func TestNewRejectsBadParams(t *testing.T) {
	if _, err := New(0, testKey, time.Hour); err == nil {
		t.Fatal("sdkAppID 为 0 应报错")
	}
	if _, err := New(testSDKAppID, "", time.Hour); err == nil {
		t.Fatal("空密钥应报错")
	}
}
