package textfilter

import "sort"

// 本文件是 Filter 的匹配内核：Aho-Corasick 多模式匹配自动机（一种带失败转移的 DFA）。
//
// 为什么从朴素匹配换成自动机：
//   - 朴素做法是"每个位置取子串查 map"，每步都要分配一个 string，长文本下 GC 压力大；
//   - 自动机对正文只做一遍扫描，每个字符沿 goto/fail 边走一步，复杂度 O(正文长度 + 命中数)，
//     与词库规模无关。词库从几百条涨到几万条时，匹配耗时几乎不变。
//
// 词库热更新时重建整棵自动机（几千条词只需几毫秒），换来的是匹配期的零分配。

type acNode struct {
	children map[rune]*acNode
	fail     *acNode
	word     *Word // 非 nil 表示从根到此节点构成一个完整词
	depth    int   // 该词的 rune 长度
}

type automaton struct {
	root *acNode
	size int
}

// match 一次命中，区间是归一化后 rune 序列的 [start, end)。
type match struct {
	start int
	end   int
	word  Word
}

func newACNode() *acNode {
	return &acNode{children: make(map[rune]*acNode, 2)}
}

// buildAC 构建自动机。words 必须已完成归一化。
func buildAC(words map[string]Word) *automaton {
	a := &automaton{root: newACNode(), size: len(words)}
	for _, w := range words {
		a.insert(w)
	}
	a.buildFail()
	return a
}

func (a *automaton) insert(w Word) {
	cur := a.root
	for _, r := range w.Text {
		next, ok := cur.children[r]
		if !ok {
			next = newACNode()
			cur.children[r] = next
		}
		cur = next
	}
	cur.word = &w
	cur.depth = len([]rune(w.Text))
}

// buildFail 用 BFS 建立失败指针：节点的 fail 指向"当前串的最长真后缀"所在节点，
// 这样匹配失败时不必回退正文，而是跳到下一个可能的位置。
func (a *automaton) buildFail() {
	queue := make([]*acNode, 0, len(a.root.children))
	for _, child := range a.root.children {
		child.fail = a.root
		queue = append(queue, child)
	}
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		for r, child := range cur.children {
			f := cur.fail
			for f != nil {
				if next, ok := f.children[r]; ok && next != child {
					child.fail = next
					break
				}
				f = f.fail
			}
			if child.fail == nil {
				child.fail = a.root
			}
			// 若 fail 节点本身是词尾，这里不复制到 child：
			// 收集命中时会沿 fail 链向上走，能拿到所有后缀词。
			queue = append(queue, child)
		}
	}
}

// find 扫描归一化后的正文，返回互不重叠的命中，按出现顺序排列。
//
// 重叠处理：同一位置命中最长的词（"傻逼"优先于"傻"，与旧实现一致）；
// 已被前一个命中覆盖的区间直接跳过，避免同一段文本被两个词重复计数。
func (a *automaton) find(runes []rune) []match {
	var out []match
	cur := a.root
	for i, r := range runes {
		// 沿 fail 链找到第一个能接受 r 的节点；都不接受则回到根，正文指针继续前进。
		for cur != a.root {
			if _, ok := cur.children[r]; ok {
				break
			}
			cur = cur.fail
		}
		next, ok := cur.children[r]
		if !ok {
			cur = a.root
			continue
		}
		cur = next
		// 沿 fail 链收集所有在当前位置结尾的词（含后缀短词）。
		for t := cur; t != nil && t != a.root; t = t.fail {
			if t.word != nil {
				out = append(out, match{start: i - t.depth + 1, end: i + 1, word: *t.word})
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].start != out[j].start {
			return out[i].start < out[j].start
		}
		return out[i].end > out[j].end // 同起点取最长
	})
	kept := out[:0]
	lastEnd := -1
	for _, m := range out {
		if m.start < lastEnd {
			continue
		}
		kept = append(kept, m)
		lastEnd = m.end
	}
	return kept
}
