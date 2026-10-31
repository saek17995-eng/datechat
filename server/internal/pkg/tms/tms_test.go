package tms

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestDisabledClient(t *testing.T) {
	c := New(Config{})
	if c.Enabled() {
		t.Fatal("无凭据时不应启用")
	}
	if _, err := c.Moderate(context.Background(), "文本", "d1", 1, "n"); err == nil {
		t.Fatal("未启用时调用应报错")
	}
	// 开关打开但缺密钥同样应禁用，避免配了开关没配密钥时静默放行。
	if New(Config{Enabled: true, SecretID: "id"}).Enabled() {
		t.Fatal("缺 SecretKey 不应启用")
	}
}

func TestEnabledClient(t *testing.T) {
	c := New(Config{Enabled: true, SecretID: "id", SecretKey: "key"})
	if !c.Enabled() {
		t.Fatal("凭据齐全应启用")
	}
}

// 响应解析契约：云端返回的字段必须能正确落到 Result 上，
// 这是唯一的外部契约，网络层本身由 tc3 包的测试覆盖（tc3 固定走 https，无法用 httptest 直接替换）。
func TestParseResponse(t *testing.T) {
	var out Result
	raw := `{"Suggestion":"Block","Label":"Porn","SubLabel":"SexualBehavior","Score":95,"Keywords":["优惠券"],"BizType":"default"}`
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !out.Blocked() || out.NeedReview() || out.Passed() {
		t.Fatalf("结论判断错误: %+v", out)
	}
	if out.Label != "Porn" || out.SubLabel != "SexualBehavior" || out.Score != 95 {
		t.Fatalf("字段解析错误: %+v", out)
	}
	if out.Score01() < 0.94 || out.Score01() > 0.96 {
		t.Fatalf("置信度归一化错误: %v", out.Score01())
	}
	if len(out.Keywords) != 1 || out.Keywords[0] != "优惠券" {
		t.Fatalf("关键词解析错误: %+v", out.Keywords)
	}
}

func TestResultKinds(t *testing.T) {
	cases := map[string]struct {
		r            Result
		block, revie bool
	}{
		"Block":  {Result{Suggestion: "Block"}, true, false},
		"Review": {Result{Suggestion: "Review"}, false, true},
		"Pass":   {Result{Suggestion: "Pass"}, false, false},
	}
	for name, c := range cases {
		if c.r.Blocked() != c.block || c.r.NeedReview() != c.revie {
			t.Errorf("%s 结论判断不符: %+v", name, c.r)
		}
	}
}

func TestTimeoutDefault(t *testing.T) {
	c := New(Config{Enabled: true, SecretID: "id", SecretKey: "key"})
	if c.cfg.Timeout != 3*time.Second {
		t.Fatalf("默认超时应为 3s，实际 %v", c.cfg.Timeout)
	}
}
