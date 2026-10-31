// Package metrics 提供不依赖外部库的最小 Prometheus 指标采集。
//
// 为什么自己写而不是引 client_golang：
// 我们只需要"接口 QPS / 延迟 / 错误率"和几条业务计数器，
// 引入完整客户端会带进来一堆用不上的采集器和构建依赖。
// 这里用分片 map + 原子计数，输出的是标准 Prometheus 文本格式，
// 直接能被 Prometheus 抓取，将来换成 client_golang 也不用改抓取配置。
package metrics

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// 分片数：后台接口与业务接口都会打点，一把全局锁在压测时会成为瓶颈。
const shards = 32

type shard struct {
	mu     sync.RWMutex
	values map[string]*counter
}

var counterShards [shards]shard

type counter struct {
	value    atomic.Int64
	labels   string
	helpText string
}

func init() {
	for i := range counterShards {
		counterShards[i].values = make(map[string]*counter)
	}
}

func shardOf(key string) *shard {
	var h uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return &counterShards[h%shards]
}

// Inc 计数器 +1。
func Inc(name string, labels ...string) {
	Add(name, 1, labels...)
}

// Add 计数器 +delta。
func Add(name string, delta int64, labels ...string) {
	key := name + labelKey(labels)
	s := shardOf(key)
	s.mu.RLock()
	c, ok := s.values[key]
	s.mu.RUnlock()
	if ok {
		c.value.Add(delta)
		return
	}
	s.mu.Lock()
	if c, ok = s.values[key]; !ok {
		c = &counter{labels: labelString(labels)}
		s.values[key] = c
	}
	s.mu.Unlock()
	c.value.Add(delta)
}

// Histogram 固定分桶的直方图。
//
// 不用 Prometheus 的动态分桶：接口延迟只需要知道"有没有超过 500ms/1s/2s"，
// 固定桶既够用又不用在运行时维护排序数组。
type Histogram struct {
	name    string
	buckets []float64 // 上界，升序
	counts  []atomic.Int64
	sum     atomic.Int64 // 微秒，避免浮点累加误差
	count   atomic.Int64
	labels  string
}

// NewHistogram 创建直方图。buckets 会在这里排序，调用方不用保证顺序。
func NewHistogram(name string, buckets []float64, labels ...string) *Histogram {
	bs := append([]float64(nil), buckets...)
	sort.Float64s(bs)
	return &Histogram{
		name:    name,
		buckets: bs,
		counts:  make([]atomic.Int64, len(bs)),
		labels:  labelString(labels),
	}
}

// Observe 记录一次观测（单位秒）。
func (h *Histogram) Observe(seconds float64) {
	us := int64(seconds * 1e6)
	h.sum.Add(us)
	h.count.Add(1)
	for i, b := range h.buckets {
		if seconds <= b {
			h.counts[i].Add(1)
		}
	}
}

var (
	histMu   sync.RWMutex
	histList []*Histogram
)

// MustHistogram 注册并返回直方图；同名同标签时返回已存在的实例。
func MustHistogram(name string, buckets []float64, labels ...string) *Histogram {
	key := name + labelKey(labels)
	histMu.RLock()
	for _, h := range histList {
		if h.name+h.labels == key {
			histMu.RUnlock()
			return h
		}
	}
	histMu.RUnlock()

	h := NewHistogram(name, buckets, labels...)
	histMu.Lock()
	histList = append(histList, h)
	histMu.Unlock()
	return h
}

// Snapshot 进程内指标快照，供 /healthz 之类的自检接口使用。
type Snapshot struct {
	Counters   map[string]int64            `json:"counters"`
	Histograms map[string]HistogramSummary `json:"histograms"`
}

// HistogramSummary 直方图摘要。
type HistogramSummary struct {
	Count  int64   `json:"count"`
	SumSec float64 `json:"sum_sec"`
	P50Sec float64 `json:"p50_sec"`
	P95Sec float64 `json:"p95_sec"`
	P99Sec float64 `json:"p99_sec"`
	MaxSec float64 `json:"max_sec"`
}

func (h *Histogram) summary() HistogramSummary {
	total := h.count.Load()
	if total == 0 {
		return HistogramSummary{}
	}
	out := HistogramSummary{
		Count:  total,
		SumSec: float64(h.sum.Load()) / 1e6,
	}
	// 用累计桶反推分位数：固定桶下这是唯一不需要保存原始样本的办法。
	cum := int64(0)
	for i, b := range h.buckets {
		cum += h.counts[i].Load()
		ratio := float64(cum) / float64(total)
		if out.P50Sec == 0 && ratio >= 0.50 {
			out.P50Sec = b
		}
		if out.P95Sec == 0 && ratio >= 0.95 {
			out.P95Sec = b
		}
		if out.P99Sec == 0 && ratio >= 0.99 {
			out.P99Sec = b
		}
		if cum >= total {
			out.MaxSec = b
			break
		}
	}
	if out.MaxSec == 0 {
		out.MaxSec = h.buckets[len(h.buckets)-1]
	}
	return out
}

// Collect 取一份当前快照（类型叫 Snapshot，函数名只能换一个）。
func Collect() Snapshot {
	out := Snapshot{
		Counters:   make(map[string]int64),
		Histograms: make(map[string]HistogramSummary),
	}
	for i := range counterShards {
		s := &counterShards[i]
		s.mu.RLock()
		for key, c := range s.values {
			out.Counters[key] = c.value.Load()
		}
		s.mu.RUnlock()
	}
	histMu.RLock()
	for _, h := range histList {
		out.Histograms[h.name+h.labels] = h.summary()
	}
	histMu.RUnlock()
	return out
}

// RenderText 输出 Prometheus 文本格式的指标。
func RenderText() string {
	var b strings.Builder
	b.WriteString("# DateChat 服务指标（进程内采集，重启清零）\n")

	type row struct {
		key   string
		name  string
		value int64
	}
	var rows []row
	for i := range counterShards {
		s := &counterShards[i]
		s.mu.RLock()
		for key, c := range s.values {
			name := strings.SplitN(key, "{", 2)[0]
			rows = append(rows, row{key: key, name: name, value: c.value.Load()})
		}
		s.mu.RUnlock()
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].key < rows[j].key })

	last := ""
	for _, r := range rows {
		if r.name != last {
			b.WriteString("# HELP " + r.name + " 计数器\n")
			b.WriteString("# TYPE " + r.name + " counter\n")
			last = r.name
		}
		b.WriteString(r.key)
		b.WriteString(" ")
		b.WriteString(strconv.FormatInt(r.value, 10))
		b.WriteString("\n")
	}

	histMu.RLock()
	defer histMu.RUnlock()
	for _, h := range histList {
		sum := h.summary()
		b.WriteString("# HELP " + h.name + " 直方图（秒）\n")
		b.WriteString("# TYPE " + h.name + " histogram\n")
		cum := int64(0)
		for i, bound := range h.buckets {
			cum += h.counts[i].Load()
			b.WriteString(h.name + "_bucket{le=\"" + formatFloat(bound) + "\"" + h.labels + "} " +
				strconv.FormatInt(cum, 10) + "\n")
		}
		b.WriteString(h.name + "_bucket{le=\"+Inf\"" + h.labels + "} " +
			strconv.FormatInt(sum.Count, 10) + "\n")
		b.WriteString(h.name + "_sum" + braces(h.labels) + " " + formatFloat(sum.SumSec) + "\n")
		b.WriteString(h.name + "_count" + braces(h.labels) + " " +
			strconv.FormatInt(sum.Count, 10) + "\n")
	}
	return b.String()
}

func braces(labels string) string {
	if labels == "" {
		return ""
	}
	return "{" + strings.Trim(labels, ",") + "}"
}

// labelKey 生成 map key：带花括号的 Prometheus 标签串。
func labelKey(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	return "{" + strings.Join(labels, ",") + "}"
}

// labelString 生成已带前导逗号的标签串，便于拼进 {} 里。
func labelString(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	out := ""
	for _, l := range labels {
		out += "," + l
	}
	return out
}

func formatFloat(v float64) string {
	if v == math.Trunc(v) {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'f', 3, 64)
}
