package textfilter

import (
	"strings"
	"testing"
)

func loadWords(t *testing.T, words ...Word) *Filter {
	t.Helper()
	f := New()
	f.Load(words)
	if f.Size() != len(words) {
		t.Fatalf("词库规模不符：期望 %d，实际 %d", len(words), f.Size())
	}
	return f
}

// 同一位置命中多个词时应保留最长的："傻逼"优先于"傻"。
// 否则短词会把长词切碎，导致"傻逼"被判成替换级而不是拦截级。
func TestACLongestMatchWins(t *testing.T) {
	f := loadWords(t,
		Word{Text: "傻", Level: LevelReplace},
		Word{Text: "傻逼", Level: LevelBlock},
	)
	got := f.Check("你这个傻逼")
	if !got.Blocked {
		t.Fatalf("应命中拦截级，实际 blocked=%v hits=%+v", got.Blocked, got.Hits)
	}
	if len(got.Hits) != 1 || got.Hits[0].Word != "傻逼" {
		t.Fatalf("只应命中「傻逼」，实际 %+v", got.Hits)
	}
}

// 一次扫描要能找出正文中分散的多个词。
func TestACMultipleMatches(t *testing.T) {
	f := loadWords(t,
		Word{Text: "微信", Level: LevelBlock},
		Word{Text: "赌博", Level: LevelBlock},
	)
	got := f.Check("加我微信，带你赌博")
	if len(got.Hits) != 2 {
		t.Fatalf("应命中 2 个词，实际 %+v", got.Hits)
	}
	if !got.Blocked {
		t.Fatal("应被拦截")
	}
}

// 失败转移要能接上跨词边界的后缀：正文 "x微信" 中"微信"起始于下标 1，
// 自动机不能因为前面有无关字符就漏掉。
func TestACMatchAfterNoise(t *testing.T) {
	f := loadWords(t, Word{Text: "微信", Level: LevelBlock})
	for _, s := range []string{"微信", "x微信", "aaa微信bbb", "加 微 信"} {
		if got := f.Check(s); !got.Blocked {
			t.Errorf("%q 应命中", s)
		}
	}
}

// 命中区间不应重叠计数：同一段文本不能被两个词重复统计。
func TestACNoOverlap(t *testing.T) {
	f := loadWords(t,
		Word{Text: "ab", Level: LevelReview},
		Word{Text: "bc", Level: LevelReview},
	)
	got := f.Check("abc")
	if len(got.Hits) != 1 {
		t.Fatalf("重叠区间只应保留一个命中，实际 %+v", got.Hits)
	}
}

// 空词库与纯噪声文本都不应命中，也不能 panic。
func TestACEmptyAndNoise(t *testing.T) {
	f := New()
	if got := f.Check("随便写点什么"); got.Blocked || len(got.Hits) != 0 {
		t.Fatal("空词库不应命中")
	}
	f2 := loadWords(t, Word{Text: "微信", Level: LevelBlock})
	if got := f2.Check("今天天气不错"); got.Blocked {
		t.Fatal("无关文本不应命中")
	}
	if got := f2.Check(""); got.Blocked || got.Text != "" {
		t.Fatal("空文本应原样返回")
	}
}

// 热更新后必须扫描新词、且不再命中已删除的词（自动机被整体重建）。
func TestACReloadRebuilds(t *testing.T) {
	f := loadWords(t, Word{Text: "微信", Level: LevelBlock})
	if !f.Check("微信").Blocked {
		t.Fatal("首次加载后应命中")
	}
	f.Load([]Word{{Text: "赌博", Level: LevelBlock}})
	if f.Check("微信").Blocked {
		t.Fatal("词库替换后旧词不应再命中")
	}
	if !f.Check("赌博").Blocked {
		t.Fatal("词库替换后新词应命中")
	}
}

// 自动机的核心承诺：耗时与词库规模无关。
// 词库从 10 条涨到 2000 条，单次匹配耗时不应成倍增长。
func BenchmarkACCheck(b *testing.B) {
	words := make([]Word, 0, 2000)
	for i := 0; i < 2000; i++ {
		words = append(words, Word{Text: "w" + strings.Repeat("x", i%8+1) + string(rune('a'+i%26)), Level: LevelReview})
	}
	f := New()
	f.Load(words)
	text := strings.Repeat("这是一段用于压测的普通文本内容，包含各种字符与标点。", 20)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = f.Check(text)
	}
}
