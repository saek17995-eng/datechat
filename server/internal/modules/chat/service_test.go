package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/datechat/server/internal/config"
)

func TestVerifyCallbackSignature(t *testing.T) {
	cfg := &config.Config{}
	cfg.IM.SdkAppID = 1400000000
	cfg.IM.CallbackToken = "callback-secret"
	cfg.IM.CallbackMaxSkew = config.Duration(time.Minute)
	fixed := time.Unix(1793433600, 0)
	svc := NewService(Deps{Config: cfg})
	svc.now = func() time.Time { return fixed }

	ts := "1793433600"
	sum := sha256.Sum256([]byte(cfg.IM.CallbackToken + ts))
	sign := hex.EncodeToString(sum[:])
	if err := svc.VerifyCallback("1400000000", sign, ts); err != nil {
		t.Fatalf("正确签名不应失败: %v", err)
	}
	if err := svc.VerifyCallback("1400000001", sign, ts); err == nil {
		t.Fatal("错误 SdkAppid 应失败")
	}
	if err := svc.VerifyCallback("1400000000", "deadbeef", ts); err == nil {
		t.Fatal("错误签名应失败")
	}
	if err := svc.VerifyCallback("1400000000", sign, "1793433000"); err == nil {
		t.Fatal("过期 RequestTime 应失败")
	}
}

func TestMessageHelpers(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"c-1793433600000-0001", true},
		{"short", false},
		{"client id with spaces", false},
		{"client/with/slash", false},
	}
	for _, tc := range cases {
		if got := validClientMessageID(tc.value); got != tc.want {
			t.Errorf("validClientMessageID(%q)=%v want %v", tc.value, got, tc.want)
		}
	}
	if got := messagePreview("  hello   world  "); got != "hello world" {
		t.Fatalf("preview=%q", got)
	}
	if got := messagePreview("这是一个用于验证会话摘要会被安全截断而不会破坏中文字符边界的超长文本消息"); len([]rune(got)) != 31 {
		t.Fatalf("长摘要应为 30 字+省略号，got=%q runes=%d", got, len([]rune(got)))
	}
}

func TestParseIMUserID(t *testing.T) {
	if uid, ok := parseIMUserID("u_123"); !ok || uid != 123 {
		t.Fatalf("解析失败 uid=%d ok=%v", uid, ok)
	}
	for _, raw := range []string{"123", "u_0", "u_-1", "u_abc", ""} {
		if _, ok := parseIMUserID(raw); ok {
			t.Fatalf("非法 UserID 不应通过: %q", raw)
		}
	}
}

func TestCallbackContent(t *testing.T) {
	kind, text := callbackContent([]MsgElement{{
		MsgType:    "TIMTextElem",
		MsgContent: map[string]any{"Text": "你好"},
	}})
	if kind != MessageTypeText || text != "你好" {
		t.Fatalf("文本解析异常 kind=%d text=%q", kind, text)
	}
	kind, text = callbackContent([]MsgElement{{MsgType: "TIMImageElem", MsgContent: map[string]any{}}})
	if kind != MessageTypeImage || text != "[图片]" {
		t.Fatalf("图片解析异常 kind=%d text=%q", kind, text)
	}
}
