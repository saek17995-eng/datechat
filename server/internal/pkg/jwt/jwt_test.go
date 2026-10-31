package jwt

import (
	"testing"
	"time"

	"github.com/datechat/server/internal/pkg/errs"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestGenerateAndParse(t *testing.T) {
	manager := NewManager(testSecret, "datechat", time.Hour, 24*time.Hour)

	token, expireAt, err := manager.Generate(10086, "device-1", TypeAccess)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if token == "" {
		t.Fatal("令牌为空")
	}
	if !expireAt.After(time.Now()) {
		t.Fatalf("过期时间应在未来，实际 %v", expireAt)
	}

	claims, err := manager.Parse(token)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if claims.UID != 10086 {
		t.Errorf("UID 期望 10086，实际 %d", claims.UID)
	}
	if claims.DeviceID != "device-1" {
		t.Errorf("DeviceID 期望 device-1，实际 %s", claims.DeviceID)
	}
	if claims.Type != TypeAccess {
		t.Errorf("Type 期望 access，实际 %s", claims.Type)
	}
}

func TestParseExpiredToken(t *testing.T) {
	// 负有效期直接产出过期令牌，避免测试里 sleep。
	manager := NewManager(testSecret, "datechat", -time.Minute, -time.Minute)

	token, _, err := manager.Generate(1, "", TypeAccess)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}

	if _, err := manager.Parse(token); !errs.Is(err, errs.CodeTokenExpired) {
		t.Fatalf("期望过期错误码 %d，实际 %v", errs.CodeTokenExpired, err)
	}
}

func TestParseTamperedToken(t *testing.T) {
	manager := NewManager(testSecret, "datechat", time.Hour, time.Hour)

	token, _, err := manager.Generate(1, "", TypeAccess)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}

	// 篡改最后一个字符，签名校验必须失败。
	tampered := token[:len(token)-1] + "x"
	if _, err := manager.Parse(tampered); !errs.Is(err, errs.CodeTokenInvalid) {
		t.Fatalf("期望非法令牌错误码 %d，实际 %v", errs.CodeTokenInvalid, err)
	}
}

func TestParseWrongSecret(t *testing.T) {
	issuer := NewManager(testSecret, "datechat", time.Hour, time.Hour)
	token, _, err := issuer.Generate(1, "", TypeAccess)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}

	other := NewManager("ffffffffffffffffffffffffffffffff", "datechat", time.Hour, time.Hour)
	if _, err := other.Parse(token); !errs.Is(err, errs.CodeTokenInvalid) {
		t.Fatalf("期望非法令牌错误码 %d，实际 %v", errs.CodeTokenInvalid, err)
	}
}
