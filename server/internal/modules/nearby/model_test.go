package nearby

import (
	"testing"
	"time"
)

// 距离模糊：对外只给区间，不暴露"她在你家 1.3 公里"。
func TestFuzzyDistance(t *testing.T) {
	cases := []struct {
		km   float64
		want string
	}{
		{0.2, "1km 以内"},
		{1, "1km 以内"},
		{2.5, "3km 以内"},
		{9.9, "10km 以内"},
		{25, "30km 以内"},
		{80, "100km 以内"},
		{500, "100km 以外"},
	}
	for _, c := range cases {
		if got := FuzzyDistance(c.km, true); got != c.want {
			t.Errorf("FuzzyDistance(%v) = %q, want %q", c.km, got, c.want)
		}
	}
	// 关闭模糊时给出一位小数的精确值，仅内网联调使用。
	if got := FuzzyDistance(1.24, false); got != "1.2km" {
		t.Errorf("FuzzyDistance(1.24,false) = %q, want 1.2km", got)
	}
	if got := FuzzyDistance(0.04, false); got != "0.0km" {
		t.Errorf("FuzzyDistance(0.04,false) = %q, want 0.0km", got)
	}
}

// 坐标校验：脏坐标进索引会让所有人的附近列表飘位。
func TestValidateCoord(t *testing.T) {
	ok := []struct{ lng, lat float64 }{
		{113.0, 22.5}, {121.47, 31.23}, {-122.4, 37.7},
	}
	for _, c := range ok {
		if !ValidateCoord(c.lng, c.lat) {
			t.Errorf("合法坐标被拒: %v,%v", c.lng, c.lat)
		}
	}
	bad := []struct{ lng, lat float64 }{
		{0, 0}, {999, 999}, {200, 22}, {113, 100}, {-181, 0},
	}
	for _, c := range bad {
		if ValidateCoord(c.lng, c.lat) {
			t.Errorf("非法坐标被放行: %v,%v", c.lng, c.lat)
		}
	}
}

// 年龄：按生日是否已过算，未填生日返回 0。
func TestAgeOf(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.Local)
	passed := time.Date(1995, 3, 1, 0, 0, 0, 0, time.Local)
	notYet := time.Date(1995, 12, 1, 0, 0, 0, 0, time.Local)
	if got := ageOf(&passed, now); got != 31 {
		t.Errorf("ageOf(1995-03-01) = %d, want 31", got)
	}
	if got := ageOf(&notYet, now); got != 30 {
		t.Errorf("ageOf(1995-12-01) = %d, want 30", got)
	}
	if got := ageOf(nil, now); got != 0 {
		t.Errorf("ageOf(nil) = %d, want 0", got)
	}
}
