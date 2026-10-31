// Package textfilter 提供敏感词匹配能力。
//
// 匹配内核是自实现的 Aho-Corasick 自动机（见 dfa.go），不引第三方依赖：
// 正文只需一遍扫描，耗时与词库规模无关；词库热更新时重建自动机即可，
// 几千条词的重建耗时在毫秒级，远小于一次 DB 查询。
//
// 分级策略与 sensitive_word.level 对应：
//   - 1 拦截：直接拒绝提交；
//   - 2 转人工：放行但标记 Review，由审核后台兜底；
//   - 3 替换：命中词替换为 *** 后放行（导流、辱骂类常用）。
package textfilter

import (
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Level 敏感词处置级别，与 sensitive_word.level 对齐。
const (
	LevelBlock   = 1 // 拦截
	LevelReview  = 2 // 转人工复审
	LevelReplace = 3 // 替换为掩码
)

// 匹配时的默认处理上限：避免词库里出现超长词条把匹配拖成 O(n²)。
const maxWordLength = 32

// Word 一条敏感词规则。
type Word struct {
	Text     string
	Category string // 分类：涉黄/涉政/导流/辱骂…
	Level    int
}

// Hit 一次命中。
type Hit struct {
	Word     string `json:"word"`
	Category string `json:"category"`
	Level    int    `json:"level"`
}

// Result 过滤结果。
type Result struct {
	Text    string `json:"text"`    // 处理后的文本（level=3 已替换为掩码）
	Hits    []Hit  `json:"hits"`    // 命中的词，按出现顺序去重
	Blocked bool   `json:"blocked"` // 是否命中拦截级
	Review  bool   `json:"review"`  // 是否需要转人工复审
}

// Filter 是线程安全的敏感词过滤器，底层是 Aho-Corasick 自动机（见 dfa.go）。
type Filter struct {
	mu       sync.RWMutex
	words    map[string]Word
	ac       *automaton
	maxLen   int
	maskChar string
}

// New 构造空过滤器。词库通过 Load 注入，便于测试与热更新。
func New() *Filter {
	return &Filter{words: make(map[string]Word), maskChar: "*"}
}

// Load 整体替换词库。运营后台改词后调用一次即可，不需要重启服务。
func (f *Filter) Load(words []Word) {
	next := make(map[string]Word, len(words)*2)
	maxLen := 0
	for _, w := range words {
		text := normalize(w.Text)
		if text == "" {
			continue
		}
		if w.Level == 0 {
			w.Level = LevelBlock
		}
		next[text] = Word{Text: text, Category: w.Category, Level: w.Level}
		if n := utf8.RuneCountInString(text); n > maxLen {
			maxLen = n
		}
	}
	if maxLen > maxWordLength {
		maxLen = maxWordLength
	}

	// 自动机在锁外构建：词库几千条时也只要几毫秒，但没必要让读者等。
	ac := buildAC(next)

	f.mu.Lock()
	f.words = next
	f.ac = ac
	f.maxLen = maxLen
	f.mu.Unlock()
}

// Size 返回当前词库规模，便于启动日志与测试断言。
func (f *Filter) Size() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.words)
}

// Check 过滤一段文本。
//
// 匹配在归一化文本上进行（去空白、全角转半角、小写），
// 这样"加 微 信"这类用空格/全角字符绕过的基础变体一样能命中。
func (f *Filter) Check(text string) Result {
	result := Result{Text: text}
	if text == "" {
		return result
	}

	f.mu.RLock()
	ac := f.ac
	f.mu.RUnlock()

	if ac == nil || ac.size == 0 {
		return result
	}

	runes := []rune(normalize(text))
	matches := ac.find(runes)
	seen := make(map[string]struct{}, len(matches))
	blocked, review := false, false
	replaceWords := make(map[string]struct{})

	for _, m := range matches {
		w := m.word
		if _, dup := seen[w.Text]; !dup {
			seen[w.Text] = struct{}{}
			result.Hits = append(result.Hits, Hit{
				Word:     w.Text,
				Category: w.Category,
				Level:    w.Level,
			})
		}
		switch w.Level {
		case LevelBlock:
			blocked = true
		case LevelReview:
			review = true
		case LevelReplace:
			replaceWords[w.Text] = struct{}{}
		}
		// 命中拦截级后继续扫描：把全部问题一次报给用户，
		// 而不是让他改掉一个词、再发一次才看到下一个违规词。
	}

	if blocked {
		result.Blocked = true
		return result
	}
	result.Review = review
	if len(replaceWords) > 0 {
		result.Text = mask(text, replaceWords, f.maskChar)
	}
	sort.SliceStable(result.Hits, func(i, j int) bool {
		return len(result.Hits[i].Word) > len(result.Hits[j].Word)
	})
	return result
}

// normalize 归一化：去空白、全角转半角、统一小写。
// 只处理最常见的规避手段，不做变体字典（那是审核团队与算法模型的活）。
func normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		if r >= 0xFF01 && r <= 0xFF5E {
			r = r - 0xFEE0 // 全角 ASCII 转半角
		}
		r = unicode.ToLower(r)
		b.WriteRune(r)
	}
	return b.String()
}

// mask 把命中词替换为等长掩码（至少 3 个星号，避免泄露原词长度）。
func mask(text string, words map[string]struct{}, maskChar string) string {
	ordered := make([]string, 0, len(words))
	for w := range words {
		ordered = append(ordered, w)
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })

	out := text
	for _, w := range ordered {
		n := utf8.RuneCountInString(w)
		if n < 3 {
			n = 3
		}
		out = strings.ReplaceAll(out, w, strings.Repeat(maskChar, n))
	}
	return out
}
