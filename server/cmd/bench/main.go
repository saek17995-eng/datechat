// Command bench 是上线前的压测工具：并发打真实接口，输出 QPS 与分位延迟。
//
// 为什么不直接用 wrk/ab：压测的是"带令牌的业务接口"，
// 先注册一批账号、拿令牌、再按真实比例混合请求，这些是 wrk 做不到的；
// 而且上线前每次都手搓 curl，跑出来的数字没有可比性。
//
// 用法：
//
//	go run ./cmd/bench -c configs/config.dev.yaml -users 50 -dur 30s
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type result struct {
	route    string
	status   int
	code     int
	duration time.Duration
	err      bool
}

func main() {
	configPath := flag.String("c", "configs/config.dev.yaml", "配置文件路径（读取 app.addr 作为压测目标）")
	base := flag.String("base", "", "压测地址，留空则用配置里的 app.addr")
	users := flag.Int("users", 50, "预热注册的账号数")
	concurrency := flag.Int("conc", 0, "并发数，0 表示与 users 相同")
	duration := flag.Duration("dur", 20*time.Second, "压测时长")
	think := flag.Duration("think", 50*time.Millisecond, "每个请求之间的思考时间，模拟真实节奏")
	rps := flag.Int("rps", 0, "全局速率上限（0 表示不限，用于压到限流器上限）")
	flag.Parse()

	target := *base
	if target == "" {
		target = addrFromConfig(*configPath)
	}
	// 配置里的 addr 常写成 ":8080"（监听所有网卡），客户端要用得补上主机名。
	if len(target) > 0 && target[0] == ':' {
		target = "127.0.0.1" + target
	}
	target = "http://" + trimScheme(target)
	if *concurrency <= 0 {
		*concurrency = *users
	}
	fmt.Printf("压测目标 %s  账号 %d  并发 %d  时长 %s\n", target, *users, *concurrency, *duration)

	tokens, err := warmup(target, *users)
	if err != nil {
		fmt.Fprintf(os.Stderr, "预热失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("预热完成：%d 个账号已登录\n", len(tokens))

	res := run(target, tokens, *concurrency, *duration, *think, *rps)
	report(res, *duration)
}

// warmup 注册并登录一批账号，拿到令牌后压测阶段直接复用。
// 不做登录压测是有意的：短信验证码在 dev 走本地通道，压它只能测出"本地通道很快"。
func warmup(base string, n int) ([]string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	out := make([]string, 0, n)
	// 手机号必须严格 11 位：直接拼前缀很容易拼出 12 位，
	// 服务端会当成非法号码，压测还没开始就全部失败。
	seed := time.Now().UnixNano() % 90_000_000
	phoneBase := int64(17_000_000_000) + seed
	for i := 0; i < n; i++ {
		phone := fmt.Sprintf("%011d", phoneBase+int64(i))
		code, err := requestCode(client, base, phone)
		if err != nil {
			return nil, fmt.Errorf("取验证码失败(%s): %w", phone, err)
		}
		token, err := login(client, base, phone, code, i)
		if err != nil {
			return nil, fmt.Errorf("登录失败(%s): %w", phone, err)
		}
		out = append(out, token)
	}
	return out, nil
}

func requestCode(c *http.Client, base, phone string) (string, error) {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(map[string]string{"phone": phone, "scene": "login"})
	resp, err := c.Post(base+"/v1/auth/sms/code", "application/json", &buf)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Code int `json:"code"`
		Data struct {
			DebugCode string `json:"debug_code"`
		} `json:"data"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("响应解析失败: %s", string(body))
	}
	if out.Data.DebugCode == "" {
		return "", fmt.Errorf("没有拿到验证码（生产环境不会下发 debug_code）: %s", string(body))
	}
	return out.Data.DebugCode, nil
}

func login(c *http.Client, base, phone, code string, idx int) (string, error) {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(map[string]any{
		"phone": phone, "code": code,
		"device_id": fmt.Sprintf("bench-%d", idx), "platform": 1, "app_version": "0.8.0",
	})
	resp, err := c.Post(base+"/v1/auth/login", "application/json", &buf)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Token struct {
				AccessToken string `json:"access_token"`
			} `json:"token"`
		} `json:"data"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("响应解析失败: %s", string(body))
	}
	if out.Data.Token.AccessToken == "" {
		return "", fmt.Errorf("登录失败 code=%d msg=%s", out.Code, out.Msg)
	}
	return out.Data.Token.AccessToken, nil
}

// scenario 一次压测迭代里的请求序列。
//
// 比例按线上真实分布给：读多写少，附近的人与钱包是最高频的两个读接口。
var scenario = []struct {
	method string
	path   string
	body   string
	weight int
}{
	{"GET", "/v1/ping", "", 1},
	{"GET", "/v1/wallet", "", 3},
	{"GET", "/v1/call/price?media_type=1", "", 2},
	{"GET", "/v1/nearby?lng=116.4074&lat=39.9042", "", 4},
	{"GET", "/v1/calls?page=1&size=20", "", 2},
	{"GET", "/v1/im/conversations?page=1&size=20", "", 2},
	{"POST", "/v1/users/me/heartbeat", "{}", 2},
}

func pick(rng *rand.Rand) int {
	total := 0
	for _, s := range scenario {
		total += s.weight
	}
	v := rng.Intn(total)
	for i, s := range scenario {
		if v < s.weight {
			return i
		}
		v -= s.weight
	}
	return 0
}

func run(base string, tokens []string, conc int, dur time.Duration, think time.Duration, rps int) []result {
	// 限速闸门：dev 环境的接口限流通常只有几十 QPS，
	// 不限速压上去拿到的全是 429，延迟数据也就没有参考价值了。
	var gate <-chan time.Time
	if rps > 0 {
		ticker := time.NewTicker(time.Second / time.Duration(rps))
		defer ticker.Stop()
		gate = ticker.C
	}
	var (
		mu   sync.Mutex
		all  []result
		stop atomic.Bool
	)
	deadline := time.Now().Add(dur)
	go func() {
		time.Sleep(dur)
		stop.Store(true)
	}()

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        conc * 4,
			MaxIdleConnsPerHost: conc * 2,
		},
	}

	var wg sync.WaitGroup
	for i := 0; i < conc; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(worker)))
			token := tokens[worker%len(tokens)]
			for !stop.Load() && time.Now().Before(deadline) {
				if gate != nil {
					<-gate
				}
				idx := pick(rng)
				s := scenario[idx]
				start := time.Now()
				status, code, failed := do(client, base, s.method, s.path, s.body, token)
				mu.Lock()
				all = append(all, result{
					route: s.path, status: status, code: code,
					duration: time.Since(start), err: failed,
				})
				mu.Unlock()
				if think > 0 {
					time.Sleep(think)
				}
			}
		}(i)
	}
	wg.Wait()
	return all
}

func do(c *http.Client, base, method, path, body, token string) (status, code int, failed bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return 0, 0, true
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, 0, true
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var parsed struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(raw, &parsed)
	// 业务码非 0 也算失败：HTTP 200 但业务拒绝，对用户来说就是没成功。
	// 401/403 这类又同时带业务码，用 || 会被统计两次，所以先看 HTTP 状态。
	if resp.StatusCode >= 400 {
		return resp.StatusCode, parsed.Code, true
	}
	return resp.StatusCode, parsed.Code, parsed.Code != 0 && parsed.Code != 401 && parsed.Code != 403
}

func report(all []result, dur time.Duration) {
	if len(all) == 0 {
		fmt.Println("没有采集到样本")
		return
	}
	lat := make([]time.Duration, 0, len(all))
	fail := 0
	failHTTP := 0
	limited := 0
	byRoute := make(map[string]int)
	for _, r := range all {
		lat = append(lat, r.duration)
		// 429 是被限流，不是服务出错：压测打满限流器是预期内的，
		// 把它算进失败率会让"通过/不通过"的判断失去意义。
		if r.status == http.StatusTooManyRequests {
			limited++
		} else if r.err {
			fail++
		}
		if r.status >= 400 {
			failHTTP++
		}
		byRoute[r.route]++
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })

	qps := float64(len(all)) / dur.Seconds()
	fmt.Printf("\n总请求 %d | QPS %.1f | 失败 %d (%.2f%%) | 被限流 %d (%.2f%%) | HTTP 错误 %d\n",
		len(all), qps, fail, float64(fail)/float64(len(all))*100,
		limited, float64(limited)/float64(len(all))*100, failHTTP)
	fmt.Printf("延迟 P50 %v | P95 %v | P99 %v | MAX %v\n",
		percentile(lat, 0.50), percentile(lat, 0.95), percentile(lat, 0.99), lat[len(lat)-1])

	fmt.Println("\n按接口：")
	keys := make([]string, 0, len(byRoute))
	for k := range byRoute {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  %-46s %d\n", k, byRoute[k])
	}

	// 结论阈值写死在工具里：压测报告如果只给数字不给结论，
	// 每次都要靠人记着"多少算合格"，迟早会漏。
	fmt.Println("\n判定（参考基线）：P95 < 300ms 且失败率 < 1%（被限流不计入）")
	p95 := percentile(lat, 0.95)
	failRate := float64(fail) / float64(len(all)) * 100
	if p95 < 300*time.Millisecond && failRate < 1 {
		fmt.Println("结论：通过")
		return
	}
	fmt.Println("结论：未通过")
	os.Exit(1)
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1)*p + 0.5)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func trimScheme(addr string) string {
	for _, p := range []string{"http://", "https://"} {
		if len(addr) > len(p) && addr[:len(p)] == p {
			return addr[len(p):]
		}
	}
	return addr
}

func addrFromConfig(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "127.0.0.1:8080"
	}
	// 只取 app.addr：不引入完整配置解析，压测工具不该因为配置项变更而编译失败。
	for _, line := range splitLines(string(raw)) {
		trimmed := trimSpace(line)
		if len(trimmed) > 4 && trimmed[:4] == "addr" {
			if i := indexOf(trimmed, ':'); i > 0 {
				v := trimSpace(trimmed[i+1:])
				v = trimQuote(v)
				if v != "" {
					return v
				}
			}
		}
	}
	return "127.0.0.1:8080"
}

func splitLines(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, cur)
	return out
}

func trimSpace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	j := len(s)
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\r') {
		j--
	}
	return s[i:j]
}

func trimQuote(s string) string {
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	return s
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
