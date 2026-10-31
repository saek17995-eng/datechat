package call

import (
	"testing"
	"time"
)

// 计费分钟数：向上取整，不足一分钟按一分钟。
func TestBillableMinutes(t *testing.T) {
	cases := []struct {
		seconds int
		want    int
	}{
		{0, 0}, {-5, 0}, {1, 1}, {59, 1}, {60, 1}, {61, 2}, {119, 2}, {120, 2}, {121, 3},
	}
	for _, c := range cases {
		if got := BillableMinutes(c.seconds); got != c.want {
			t.Errorf("BillableMinutes(%d) = %d, want %d", c.seconds, got, c.want)
		}
	}
}

// 抽成拆分：收益 + 抽成必须恒等于总额，不能因为取整把金币弄丢。
func TestPriceSplit(t *testing.T) {
	cases := []struct {
		rate       int
		total      int64
		wantIncome int64
		wantFee    int64
	}{
		{0, 100, 100, 0},
		{300, 100, 70, 30},
		{300, 50, 35, 15},
		{1000, 50, 0, 50},
		{333, 100, 67, 33},
		{1, 999, 999, 0},
	}
	for _, c := range cases {
		p := Price{PerMin: 10, PlatformRate: c.rate}
		income, fee := p.Split(c.total)
		if income != c.wantIncome || fee != c.wantFee {
			t.Errorf("rate=%d Split(%d) = (%d,%d), want (%d,%d)",
				c.rate, c.total, income, fee, c.wantIncome, c.wantFee)
		}
		if income+fee != c.total {
			t.Errorf("rate=%d Split(%d) 收益+抽成=%d，与总额不符", c.rate, c.total, income+fee)
		}
	}
}

// 切片幂等键：同一通电话同一分钟必须得到同一个 biz_id，
// 否则重复心跳会在账本里写出两条流水。
func TestSegmentBizID(t *testing.T) {
	a := SegmentBizID("C20261031120000123456", 3)
	b := SegmentBizID("C20261031120000123456", 3)
	c := SegmentBizID("C20261031120000123456", 4)
	if a != b {
		t.Fatalf("同一分钟幂等键不一致: %s vs %s", a, b)
	}
	if a == c {
		t.Fatalf("不同分钟幂等键相同: %s", a)
	}
	if a != "C20261031120000123456#3" {
		t.Fatalf("幂等键格式异常: %s", a)
	}
}

// 通话单号：时间前缀 + 6 位随机，长度固定便于人工核对客诉。
func TestNewCallNo(t *testing.T) {
	no := NewCallNo(time.Now(), randomUint32())
	// 1 位前缀 + 14 位时间 + 6 位随机 = 21
	if len(no) != 21 {
		t.Fatalf("单号长度异常: %s(%d)", no, len(no))
	}
	if no[0] != 'C' {
		t.Fatalf("单号前缀异常: %s", no)
	}
}

// 参与方与对端判定：账单、权限都依赖它，判错会把别人的通话暴露出去。
func TestSessionPeer(t *testing.T) {
	s := &Session{CallerID: 1, CalleeID: 2}
	if !s.IsParticipant(1) || !s.IsParticipant(2) || s.IsParticipant(3) {
		t.Fatal("参与方判定错误")
	}
	if s.PeerOf(1) != 2 || s.PeerOf(2) != 1 {
		t.Fatal("对端判定错误")
	}
}

// 终态判定：进入终态后不允许再跃迁，否则会出现"挂断后又接通"的双花。
func TestIsFinal(t *testing.T) {
	for _, st := range []int{StatusEnded, StatusCanceled, StatusRejected, StatusTimeout, StatusAbnormal} {
		if !isFinal(st) {
			t.Errorf("status=%d 应为终态", st)
		}
	}
	for _, st := range []int{StatusInit, StatusRinging, StatusTalking} {
		if isFinal(st) {
			t.Errorf("status=%d 不应为终态", st)
		}
	}
}
