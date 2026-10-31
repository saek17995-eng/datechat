package phone

import "testing"

func TestNormalize(t *testing.T) {
	valid := map[string]string{
		"13800138000":       "13800138000",
		"138 0013 8000":     "13800138000",
		"138-0013-8000":     "13800138000",
		"+8613800138000":    "13800138000",
		"+86 138-0013-8000": "13800138000",
		"008613800138000":   "13800138000",
		"8613800138000":     "13800138000",
		"\t19912345678 \n":  "19912345678",
	}
	for input, want := range valid {
		got, err := Normalize(input)
		if err != nil {
			t.Fatalf("归一化 %q 失败: %v", input, err)
		}
		if got != want {
			t.Fatalf("归一化 %q 期望 %q 实际 %q", input, want, got)
		}
	}
}

func TestNormalizeRejectsInvalid(t *testing.T) {
	invalid := []string{
		"",
		"1380013800",    // 10 位
		"138001380000",  // 12 位
		"12800138000",   // 第二位为 2，非法号段
		"23800138000",   // 不以 1 开头
		"1380013800a",   // 含字母
		"+1 4155552671", // 境外号码
	}
	for _, input := range invalid {
		if got, err := Normalize(input); err == nil {
			t.Fatalf("非法号码 %q 应被拒绝，实际返回 %q", input, got)
		}
	}
}

func TestMask(t *testing.T) {
	if got := Mask("13800138000"); got != "138****8000" {
		t.Fatalf("脱敏结果期望 138****8000，实际 %s", got)
	}
	if got := Mask("123"); got != "***" {
		t.Fatalf("非法长度应返回 ***，实际 %s", got)
	}
}
