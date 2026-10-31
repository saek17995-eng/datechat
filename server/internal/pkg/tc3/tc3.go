// Package tc3 实现腾讯云 API 3.0（TC3-HMAC-SHA256）签名与调用。
//
// 短信、人脸核身等多条产品线共用同一套签名，所以抽成独立包，业务侧只关心 Action 与参数。
// 只依赖标准库：避免为一次 HTTP 调用拖入整套 SDK。
package tc3

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Credential 是腾讯云访问密钥。
type Credential struct {
	SecretID  string
	SecretKey string
}

// Valid 判断密钥是否已配置。
func (c Credential) Valid() bool { return c.SecretID != "" && c.SecretKey != "" }

// APIError 是腾讯云返回的业务错误（HTTP 200 但 Response.Error 非空）。
type APIError struct {
	Code    string `json:"Code"`
	Message string `json:"Message"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("腾讯云接口错误 %s: %s", e.Code, e.Message)
}

// Client 绑定一个产品线（service + endpoint）。
type Client struct {
	cred    Credential
	service string
	host    string
	http    *http.Client
}

// New 创建客户端。service 例如 "sms"、"faceid"；host 例如 "sms.tencentcloudapi.com"。
func New(cred Credential, service, host string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Client{
		cred:    cred,
		service: service,
		host:    host,
		http:    &http.Client{Timeout: timeout},
	}
}

// Call 调用一个 Action，返回 Response 对象的原始 JSON（已剔除外层 Response 包装）。
func (c *Client) Call(ctx context.Context, action, version, region string, payload any) (json.RawMessage, error) {
	if !c.cred.Valid() {
		return nil, fmt.Errorf("tc3: 未配置 secret_id / secret_key")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("tc3: 序列化请求失败: %w", err)
	}

	now := time.Now().UTC()
	date := now.Format("2006-01-02")
	ts := strconv.FormatInt(now.Unix(), 10)
	auth, err := c.authorization(action, ts, date, body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+c.host, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("tc3: 构造请求失败: %w", err)
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Timestamp", ts)
	req.Header.Set("X-TC-Version", version)
	if region != "" {
		req.Header.Set("X-TC-Region", region)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tc3: 请求 %s 失败: %w", action, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("tc3: 读取响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tc3: %s 返回 HTTP %d: %s", action, resp.StatusCode, truncate(string(raw), 200))
	}

	var top struct {
		Response json.RawMessage `json:"Response"`
	}
	if err := json.Unmarshal(raw, &top); err != nil || len(top.Response) == 0 {
		return nil, fmt.Errorf("tc3: 响应结构异常: %s", truncate(string(raw), 200))
	}

	var probe struct {
		Error     *APIError `json:"Error"`
		RequestID string    `json:"RequestId"`
	}
	if err := json.Unmarshal(top.Response, &probe); err != nil {
		return nil, fmt.Errorf("tc3: 解析 Response 失败: %w", err)
	}
	if probe.Error != nil {
		return top.Response, &APIError{
			Code:    probe.Error.Code,
			Message: probe.Error.Message + " (RequestId=" + probe.RequestID + ")",
		}
	}
	return top.Response, nil
}

const contentType = "application/json; charset=utf-8"

// authorization 按官方规范拼 Authorization 头。
//
//	canonicalRequest = method\nuri\nquery\ncanonicalHeaders\nsignedHeaders\nhashedPayload
//	stringToSign     = TC3-HMAC-SHA256\ntimestamp\nscope\nhash(canonicalRequest)
func (c *Client) authorization(action, ts, date string, body []byte) (string, error) {
	canonicalHeaders := fmt.Sprintf("content-type:%s\nhost:%s\nx-tc-action:%s\n",
		contentType, c.host, strings.ToLower(action))
	signedHeaders := "content-type;host;x-tc-action"

	canonicalRequest := strings.Join([]string{
		http.MethodPost,
		"/",
		"",
		canonicalHeaders,
		signedHeaders,
		sha256Hex(body),
	}, "\n")

	scope := date + "/" + c.service + "/tc3_request"
	stringToSign := strings.Join([]string{
		"TC3-HMAC-SHA256",
		ts,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	secretDate := hmacSHA256([]byte("TC3"+c.cred.SecretKey), date)
	secretService := hmacSHA256(secretDate, c.service)
	secretSigning := hmacSHA256(secretService, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))

	return fmt.Sprintf("TC3-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		c.cred.SecretID, scope, signedHeaders, signature), nil
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// truncate 截断日志内容，避免超长响应体污染日志。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
