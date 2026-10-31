package idcard

import (
	"errors"
	"testing"
	"time"
)

// 110101199003071233 是按 GB 11643 校验位规则推算出的合法样例，仅用于测试。
const validID = "110101199003071233"

func TestValidate(t *testing.T) {
	if err := Validate(validID); err != nil {
		t.Fatalf("合法身份证号校验失败: %v", err)
	}
	// 校验位为 X 的号码，且小写 x 也应通过（先归一化为大写）。
	const xID = "11010119900307125X"
	if err := Validate(xID); err != nil {
		t.Fatalf("校验位为 X 的合法号码校验失败: %v", err)
	}
	if err := Validate("11010119900307125x"); err != nil {
		t.Fatalf("小写 x 应被归一化后通过: %v", err)
	}
}

func TestValidateRejectsBadChecksum(t *testing.T) {
	bad := validID[:17] + "4"
	err := Validate(bad)
	if !errors.Is(err, ErrInvalidChecksum) {
		t.Fatalf("校验位错误应返回 ErrInvalidChecksum，实际 %v", err)
	}
}

func TestValidateRejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"长度不足":   "11010119900307",
		"15 位旧版": "110101900307123",
		"含字母":    "11010119900307123A",
		"省份非法":   "990101199003071233",
		"日期非法":   "110101199013071233", // 13 月
		"未来日期":   "110101209903071233",
	}
	for name, id := range cases {
		if err := Validate(id); err == nil {
			t.Fatalf("%s 的号码 %q 应被拒绝", name, id)
		}
	}
}

func TestBirthDateAndAge(t *testing.T) {
	born, err := BirthDate(validID)
	if err != nil {
		t.Fatalf("解析出生日期失败: %v", err)
	}
	if born.Format("2006-01-02") != "1990-03-07" {
		t.Fatalf("出生日期期望 1990-03-07，实际 %s", born.Format("2006-01-02"))
	}

	age, err := Age(validID, time.Date(2026, 3, 6, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("计算年龄失败: %v", err)
	}
	if age != 35 {
		t.Fatalf("生日前一天年龄应为 35，实际 %d", age)
	}

	age, err = Age(validID, time.Date(2026, 3, 7, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("计算年龄失败: %v", err)
	}
	if age != 36 {
		t.Fatalf("生日当天年龄应为 36，实际 %d", age)
	}
}

func TestGender(t *testing.T) {
	// 第 17 位是 3（奇数）→ 男。
	if got := Gender(validID); got != 1 {
		t.Fatalf("性别期望 1（男），实际 %d", got)
	}
	if got := Gender(validID[:16] + "4" + validID[17:]); got != 2 {
		t.Fatalf("第 17 位偶数应为女（2），实际 %d", got)
	}
	if got := Gender("bad"); got != 0 {
		t.Fatalf("非法输入应返回 0，实际 %d", got)
	}
}

func TestMask(t *testing.T) {
	want := "110101********1233"
	if got := Mask(validID); got != want {
		t.Fatalf("脱敏期望 %s 实际 %s", want, got)
	}
	if got := Mask("123"); got != "***" {
		t.Fatalf("非法长度应返回 ***，实际 %s", got)
	}
}
