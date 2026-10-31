package profile

import (
	"testing"
	"time"

	"github.com/datechat/server/internal/config"
	"github.com/datechat/server/internal/pkg/textfilter"
)

// newTestService 构造只依赖常量的服务，用于测试纯逻辑（校验、换算、URL 反解）。
// 故意不注入 repo / redis：这些测试不应该连数据库或缓存。
func newTestService(t *testing.T) *Service {
	t.Helper()
	cfg := &config.Config{}
	cfg.Upload.Provider = "local"
	cfg.Upload.MaxImageMB = 5
	cfg.Upload.Local.Dir = "./.local/uploads"
	cfg.Upload.Local.URLPrefix = "/static/uploads"
	cfg.Upload.COS.Prefix = "upload/"
	return NewService(Deps{Config: cfg, Filter: sensitiveFilter()})
}

func sensitiveFilter() *textfilter.Filter {
	f := textfilter.New()
	f.Load([]textfilter.Word{
		{Text: "加我微信", Category: "导流", Level: textfilter.LevelBlock},
		{Text: "傻逼", Category: "辱骂", Level: textfilter.LevelReplace},
	})
	return f
}

func TestCalcAge(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.Local)
	cases := []struct {
		birth string
		want  int
	}{
		{"1995-06-18", 31}, // 今年已过生日
		{"1995-12-18", 30}, // 今年还没到生日
		{"2008-10-31", 18}, // 正好成年
	}
	for _, c := range cases {
		birth, err := time.ParseInLocation("2006-01-02", c.birth, time.Local)
		if err != nil {
			t.Fatalf("解析生日失败: %v", err)
		}
		if got := calcAge(birth, now); got != c.want {
			t.Fatalf("%s → %d 岁，期望 %d", c.birth, got, c.want)
		}
	}
}

func TestEffectivePrice(t *testing.T) {
	global := &GlobalPrice{AudioPerMin: 50, VideoPerMin: 120}
	// 未开启自定价：跟随全局
	if a, v := effectivePrice(&UserProfile{}, global); a != 50 || v != 120 {
		t.Fatalf("应为全局价，实际 %d/%d", a, v)
	}
	// 开启但自定价为 0：仍跟随全局
	if a, v := effectivePrice(&UserProfile{PriceEnabled: 1}, global); a != 50 || v != 120 {
		t.Fatalf("自定价为 0 时应跟随全局，实际 %d/%d", a, v)
	}
	// 开启且自定价有效
	p := &UserProfile{PriceEnabled: 1, PriceAudio: 80, PriceVideo: 200}
	if a, v := effectivePrice(p, global); a != 80 || v != 200 {
		t.Fatalf("应为自定价，实际 %d/%d", a, v)
	}
}

func TestAllowedFileURL(t *testing.T) {
	s := newTestService(t)
	cases := []struct {
		url  string
		want bool
	}{
		{"/static/uploads/upload/avatar/1/20261031/a.png", true},
		{"http://127.0.0.1:8080/static/uploads/upload/avatar/1/a.png", true},
		{"https://evil.example.com/a.png", false},
		{"/static/uploads/../etc/passwd", false}, // 目录穿越
		{"", false},
		{"javascript:alert(1)", false},
	}
	for _, c := range cases {
		if got := s.allowedFileURL(c.url); got != c.want {
			t.Fatalf("allowedFileURL(%q)=%v，期望 %v", c.url, got, c.want)
		}
	}
}

func TestObjectKeyOf(t *testing.T) {
	s := newTestService(t)
	cases := map[string]string{
		"/static/uploads/upload/avatar/1/20261031/a.png":  "upload/avatar/1/20261031/a.png",
		"http://host/static/uploads/upload/photo/2/b.jpg": "upload/photo/2/b.jpg",
		"/static/uploads/upload/voice/3/c%20d.m4a":        "upload/voice/3/c d.m4a",
	}
	for in, want := range cases {
		if got := s.objectKeyOf(in); got != want {
			t.Fatalf("objectKeyOf(%q)=%q，期望 %q", in, got, want)
		}
	}
}

func TestSanitizeTags(t *testing.T) {
	s := newTestService(t)
	// 去重 + 限长 + 上限
	tags, err := s.sanitizeTags([]string{"温柔", "温柔", " 咖啡 ", "爱旅行"})
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(tags) != 3 || tags[0] != "温柔" || tags[1] != "咖啡" {
		t.Fatalf("清洗结果不符合预期: %v", tags)
	}

	// 超长标签应被拒绝
	if _, err := s.sanitizeTags([]string{"这是一个非常长的标签"}); err == nil {
		t.Fatal("超过 8 个字的标签应报错")
	}

	// 拦截级敏感词
	if _, err := s.sanitizeTags([]string{"加我微信"}); err == nil {
		t.Fatal("拦截级敏感词应报错")
	}

	// 替换级敏感词：不报错但内容被打码
	got, err := s.sanitizeTags([]string{"傻逼"})
	if err != nil {
		t.Fatalf("替换级不应报错: %v", err)
	}
	if len(got) != 1 || got[0] != "***" {
		t.Fatalf("替换级应打码，实际 %v", got)
	}

	// 超过上限只保留前 maxTags 个
	many := make([]string, 0, maxTags+3)
	for i := 0; i < maxTags+3; i++ {
		many = append(many, string(rune('A'+i)))
	}
	got, err = s.sanitizeTags(many)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(got) != maxTags {
		t.Fatalf("标签数量应截断到 %d，实际 %d", maxTags, len(got))
	}
}

func TestLastActiveText(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.Local)
	recent := now.Add(-15 * time.Minute)
	if got := lastActiveText(true, &recent, now); got != "在线" {
		t.Fatalf("在线优先，实际 %q", got)
	}
	if got := lastActiveText(false, &recent, now); got != "15 分钟前活跃" {
		t.Fatalf("分钟级文案不正确: %q", got)
	}
	if got := lastActiveText(false, nil, now); got != "离线" {
		t.Fatalf("无活跃时间应为离线，实际 %q", got)
	}
}
