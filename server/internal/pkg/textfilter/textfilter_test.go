package textfilter

import "testing"

func TestFilterBlock(t *testing.T) {
	f := New()
	f.Load([]Word{
		{Text: "微信", Category: "导流", Level: LevelBlock},
		{Text: "weixin", Category: "导流", Level: LevelBlock},
		{Text: "傻逼", Category: "辱骂", Level: LevelReplace},
		{Text: "兼职", Category: "广告", Level: LevelReview},
	})

	cases := []struct {
		name        string
		in          string
		wantBlocked bool
		wantReview  bool
		wantText    string
		wantHits    int
	}{
		{"正常文本", "你好，很高兴认识你", false, false, "你好，很高兴认识你", 0},
		{"拦截级", "加我微信聊聊", true, false, "加我微信聊聊", 1},
		{"替换级", "你是个傻逼", false, false, "你是个***", 1},
		{"转人工级", "兼职日入过千", false, true, "兼职日入过千", 1},
		{"空格绕过", "加 微 信", true, false, "加 微 信", 1},
		{"全角绕过", "加我ＷＥＩＸＩＮ", true, false, "加我ＷＥＩＸＩＮ", 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := f.Check(c.in)
			if got.Blocked != c.wantBlocked {
				t.Fatalf("Blocked=%v，期望 %v（text=%q）", got.Blocked, c.wantBlocked, got.Text)
			}
			if got.Review != c.wantReview {
				t.Fatalf("Review=%v，期望 %v", got.Review, c.wantReview)
			}
			if got.Text != c.wantText {
				t.Fatalf("Text=%q，期望 %q", got.Text, c.wantText)
			}
			if len(got.Hits) != c.wantHits {
				t.Fatalf("命中 %d 条，期望 %d：%+v", len(got.Hits), c.wantHits, got.Hits)
			}
		})
	}
}

func TestFilterEmptyAndReload(t *testing.T) {
	f := New()
	if got := f.Check("任意内容"); got.Blocked || got.Text != "任意内容" {
		t.Fatalf("空词库不应改变文本：%+v", got)
	}
	f.Load([]Word{{Text: "测试词", Level: LevelBlock}})
	if f.Size() != 1 {
		t.Fatalf("词库规模应为 1，实际 %d", f.Size())
	}
	f.Load(nil)
	if f.Size() != 0 {
		t.Fatalf("重新加载空词库后应为 0，实际 %d", f.Size())
	}
}

func TestFilterMaskAtLeastThreeStars(t *testing.T) {
	f := New()
	f.Load([]Word{{Text: "黄", Level: LevelReplace}})
	got := f.Check("黄")
	if got.Text != "***" {
		t.Fatalf("单字命中也至少掩码 3 位，实际 %q", got.Text)
	}
}
