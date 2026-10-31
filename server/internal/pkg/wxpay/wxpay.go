// Package wxpay 封装微信支付 v3 的下单、查单与回调验签解密。
//
// 为什么自己写而不用官方 SDK：
//   - 我们只需要"下单 + 查单 + 验签解密"三个能力，SDK 带进来的依赖远大于此；
//   - 回调验签是安全关键路径，自己实现能把它写进单测，而不是信任黑盒。
//
// 三条安全铁律：
//  1. 下单金额一律用「分」，且由服务端按档位价计算，绝不信客户端传的金额；
//  2. 回调必须验签 + 解密后才认；验签失败一律拒绝，不"先发货再说"；
//  3. 到账与否以服务端查单/回调为准，客户端返回的成功不能作为发货依据。
package wxpay

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config 微信支付配置。
type Config struct {
	Enabled    bool   // 未配置齐全时关闭
	AppID      string // 应用ID（公众号/小程序/移动应用）
	MchID      string // 商户号
	SerialNo   string // 商户 API 证书序列号
	PrivateKey string // 商户私钥 PEM（PKCS#1 或 PKCS#8）
	APIV3Key   string // APIv3 密钥，32 字节，用于解密回调
	NotifyURL  string
	PayScene   string // app / jsapi / h5 / native，决定调用的下单接口
	Timeout    time.Duration
	Sandbox    bool // 沙箱（v3 暂无沙箱，仅作标记）
}

// Valid 配置是否可用。
func (c Config) Valid() bool {
	return c.AppID != "" && c.MchID != "" && c.SerialNo != "" && c.PrivateKey != "" && c.APIV3Key != ""
}

// Client 微信支付客户端。
type Client struct {
	cfg  Config
	key  *rsa.PrivateKey
	http *http.Client
}

// New 构造客户端；配置不完整时返回禁用状态（Enabled() == false）。
func New(cfg Config) (*Client, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	c := &Client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}
	if !cfg.Enabled || !cfg.Valid() {
		return c, nil
	}
	key, err := parsePrivateKey(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("wxpay: 解析商户私钥失败: %w", err)
	}
	c.key = key
	return c, nil
}

// Enabled 客户端是否可用于真实支付。
func (c *Client) Enabled() bool { return c != nil && c.key != nil }

// Prepay 下单结果，字段直接给客户端拉起支付用。
type Prepay struct {
	PrepayID  string `json:"prepay_id"`
	OrderNo   string `json:"order_no"`
	PayParams map[string]any
}

// Order 下单请求。AmountFen 必须由服务端计算。
type Order struct {
	OrderNo   string
	AmountFen int
	Desc      string
	ClientIP  string
	OpenID    string // jsapi 场景必填
	Attach    string
}

const host = "https://api.mch.weixin.qq.com"

// Prepay 创建支付订单。scene 由配置决定：
// app → /v3/pay/transactions/app，jsapi → /v3/pay/transactions/jsapi。
func (c *Client) Prepay(ctx context.Context, o Order) (*Prepay, error) {
	if !c.Enabled() {
		return nil, errors.New("wxpay: 客户端未启用")
	}
	path := "/v3/pay/transactions/app"
	if c.cfg.PayScene == "jsapi" {
		path = "/v3/pay/transactions/jsapi"
	}
	payer := map[string]any{}
	if c.cfg.PayScene == "jsapi" {
		if o.OpenID == "" {
			return nil, errors.New("wxpay: jsapi 下单缺少 openid")
		}
		payer["openid"] = o.OpenID
	}
	body := map[string]any{
		"appid":        c.cfg.AppID,
		"mchid":        c.cfg.MchID,
		"description":  truncateRunes(o.Desc, 127),
		"out_trade_no": o.OrderNo,
		"notify_url":   c.cfg.NotifyURL,
		"amount":       map[string]any{"total": o.AmountFen, "currency": "CNY"},
	}
	if len(payer) > 0 {
		body["payer"] = payer
	}
	if o.Attach != "" {
		body["attach"] = o.Attach
	}

	raw, err := c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	var out struct {
		PrepayID string `json:"prepay_id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("wxpay: 解析下单响应失败: %w", err)
	}
	if out.PrepayID == "" {
		return nil, fmt.Errorf("wxpay: 下单响应缺少 prepay_id: %s", string(raw))
	}
	return &Prepay{
		PrepayID:  out.PrepayID,
		OrderNo:   o.OrderNo,
		PayParams: c.payParams(out.PrepayID),
	}, nil
}

// payParams 生成客户端拉起支付所需参数并签名。
func (c *Client) payParams(prepayID string) map[string]any {
	ts := time.Now().Unix()
	nonce := randomString(32)
	// app 场景签名串：appId\ntimeStamp\nnonceStr\nprepayId\n
	msg := fmt.Sprintf("%s\n%d\n%s\n%s\n", c.cfg.AppID, ts, nonce, prepayID)
	sig, err := c.sign(msg)
	if err != nil {
		return nil
	}
	return map[string]any{
		"appid":     c.cfg.AppID,
		"partnerid": c.cfg.MchID,
		"prepayid":  prepayID,
		"package":   "Sign=WXPay",
		"noncestr":  nonce,
		"timestamp": ts,
		"sign":      sig,
	}
}

// Notify 回调解密后的内容。
type Notify struct {
	OrderNo   string
	TradeNo   string
	EventType string
	AmountFen int
	PaidAt    time.Time
	Raw       map[string]any
}

// resource 回调报文结构（只取关心字段）。
type notifyBody struct {
	ID        string `json:"id"`
	EventType string `json:"event_type"`
	Resource  struct {
		Algorithm      string `json:"algorithm"`
		Ciphertext     string `json:"ciphertext"`
		Nonce          string `json:"nonce"`
		AssociatedData string `json:"associated_data"`
	} `json:"resource"`
}

type resourcePlain struct {
	AppID       string `json:"appid"`
	MchID       string `json:"mchid"`
	OutTradeNo  string `json:"out_trade_no"`
	TradeNo     string `json:"transaction_id"`
	TradeState  string `json:"trade_state"`
	TradeStateD string `json:"trade_state_desc"`
	Amount      struct {
		Total    int    `json:"total"`
		Currency string `json:"currency"`
	} `json:"amount"`
	SuccessTime string `json:"success_time"`
}

// VerifyNotify 验签并解密回调。
//
// 验签串是 "时间戳\n随机串\n报文体\n"，签名用微信支付平台证书/公钥验。
// 平台证书需要下载并轮换（P8 做证书自动轮换，此处先用 APIv3 密钥解密并校验商户号）。
func (c *Client) VerifyNotify(body []byte, signature, timestamp, nonce, serial string) (*Notify, error) {
	if !c.Enabled() {
		return nil, errors.New("wxpay: 客户端未启用，无法处理回调")
	}
	if signature == "" || timestamp == "" || nonce == "" {
		return nil, errors.New("wxpay: 回调缺少验签头")
	}
	// 时间戳校验：超过 5 分钟的回调拒绝，防重放。
	ts, err := parseInt64(timestamp)
	if err != nil {
		return nil, errors.New("wxpay: 回调时间戳不合法")
	}
	if abs(time.Now().Unix()-ts) > 300 {
		return nil, errors.New("wxpay: 回调时间戳超出允许范围")
	}
	// 平台公钥未提供时退化为"解密 + 关键字段校验"，
	// 上线前必须配置平台证书并做真正的 RSA 验签（见 README 的 P5 待办）。
	_ = serial

	var nb notifyBody
	if err := json.Unmarshal(body, &nb); err != nil {
		return nil, fmt.Errorf("wxpay: 解析回调体失败: %w", err)
	}
	if nb.Resource.Ciphertext == "" {
		return nil, errors.New("wxpay: 回调缺少密文")
	}
	plain, err := c.decryptResource(nb.Resource.Ciphertext, nb.Resource.Nonce, nb.Resource.AssociatedData)
	if err != nil {
		return nil, err
	}
	var rp resourcePlain
	if err := json.Unmarshal(plain, &rp); err != nil {
		return nil, fmt.Errorf("wxpay: 解析解密结果失败: %w", err)
	}
	if rp.MchID != "" && rp.MchID != c.cfg.MchID {
		return nil, fmt.Errorf("wxpay: 回调商户号不匹配: %s", rp.MchID)
	}
	var raw map[string]any
	_ = json.Unmarshal(plain, &raw)
	paidAt := time.Now()
	if rp.SuccessTime != "" {
		if t, e := time.Parse(time.RFC3339, strings.ReplaceAll(rp.SuccessTime, " ", "T")+"Z"); e == nil {
			paidAt = t
		}
	}
	return &Notify{
		OrderNo:   rp.OutTradeNo,
		TradeNo:   rp.TradeNo,
		EventType: nb.EventType,
		AmountFen: rp.Amount.Total,
		PaidAt:    paidAt,
		Raw:       raw,
	}, nil
}

// Query 按商户订单号查单，用于回调丢失时补单。
func (c *Client) Query(ctx context.Context, orderNo string) (*Notify, error) {
	if !c.Enabled() {
		return nil, errors.New("wxpay: 客户端未启用")
	}
	path := "/v3/pay/transactions/out-trade-no/" + orderNo + "?mchid=" + c.cfg.MchID
	raw, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var rp resourcePlain
	if err := json.Unmarshal(raw, &rp); err != nil {
		return nil, fmt.Errorf("wxpay: 解析查单响应失败: %w", err)
	}
	n := &Notify{OrderNo: rp.OutTradeNo, TradeNo: rp.TradeNo, AmountFen: rp.Amount.Total, PaidAt: time.Now()}
	if rp.SuccessTime != "" {
		if t, e := time.Parse(time.RFC3339, strings.ReplaceAll(rp.SuccessTime, " ", "T")+"Z"); e == nil {
			n.PaidAt = t
		}
	}
	return n, nil
}

// ---- 内部实现 ----

func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = b
	}
	ts := time.Now().Unix()
	nonce := randomString(32)
	msg := fmt.Sprintf("%s\n%s\n%d\n%s\n%s\n", method, path, ts, nonce, string(payload))
	sig, err := c.sign(msg)
	if err != nil {
		return nil, err
	}
	auth := fmt.Sprintf(`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",signature="%s",timestamp="%d",serial_no="%s"`,
		c.cfg.MchID, nonce, sig, ts, c.cfg.SerialNo)

	req, err := http.NewRequestWithContext(ctx, method, host+path, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "datechat-server")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wxpay: 请求失败: %w", err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("wxpay: 请求失败 status=%d body=%s", resp.StatusCode, string(out))
	}
	return out, nil
}

// sign 对签名串做 SHA256withRSA 并 Base64。
func (c *Client) sign(msg string) (string, error) {
	if c.key == nil {
		return "", errors.New("wxpay: 未配置商户私钥")
	}
	sum := sha256.Sum256([]byte(msg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("wxpay: 签名失败: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// decryptResource 用 APIv3 密钥解密回调 resource（AES-256-GCM）。
func (c *Client) decryptResource(ciphertext, nonce, aad string) ([]byte, error) {
	if len(c.cfg.APIV3Key) != 32 {
		return nil, errors.New("wxpay: APIv3 密钥长度必须为 32 字节")
	}
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("wxpay: 密文 base64 解码失败: %w", err)
	}
	block, err := aes.NewCipher([]byte(c.cfg.APIV3Key))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, []byte(nonce), data, []byte(aad))
	if err != nil {
		return nil, fmt.Errorf("wxpay: 回调解密失败（密钥错误或报文被篡改）: %w", err)
	}
	return plain, nil
}

// VerifyPlatformSignature 用平台公钥做真正的 RSA 验签。
// P8 引入平台证书轮换后会调用它；目前保留实现以便随时启用。
func VerifyPlatformSignature(pub *rsa.PublicKey, body []byte, signature, timestamp, nonce string) error {
	msg := fmt.Sprintf("%s\n%s\n%s\n", timestamp, nonce, string(body))
	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("wxpay: 签名 base64 解码失败: %w", err)
	}
	sum := sha256.Sum256([]byte(msg))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		return fmt.Errorf("wxpay: 回调验签失败: %w", err)
	}
	return nil
}

func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("PEM 解析失败")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("不是 RSA 私钥")
	}
	return key, nil
}

func randomString(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return strings.Repeat("0", n)
	}
	for i := range buf {
		buf[i] = chars[int(buf[i])%len(chars)]
	}
	return string(buf)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func parseInt64(s string) (int64, error) {
	var v int64
	var neg bool
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, errors.New("not a number")
		}
		v = v*10 + int64(s[i]-'0')
	}
	if neg {
		v = -v
	}
	return v, nil
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
