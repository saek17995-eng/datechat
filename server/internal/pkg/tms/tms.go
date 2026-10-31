// Package tms 封装腾讯云文本内容安全（TMS）的文本审核接口。
//
// 定位：本地 DFA 敏感词是"快、免费、可解释"的第一道防线，云审核负责
// 语义级识别（变体、拼音、拆字、隐晦导流），两者互补而不是替代。
//
// 与本地过滤最大的不同是**这是一次网络调用**，所以本包刻意做成：
//   - 无凭据时 Enabled() 为 false，调用方直接跳过，不会因缺配置而报错；
//   - 超时与降级策略由调用方决定（见 moderation 模块），本包只如实返回结果或错误。
package tms

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/datechat/server/internal/pkg/tc3"
)

// 接口约定（腾讯云文本内容安全 2020-12-29 版）。
const (
	action  = "TextModeration"
	version = "2020-12-29"
	service = "tms"
)

// Config 云审核配置。
type Config struct {
	Enabled   bool
	SecretID  string
	SecretKey string
	Region    string
	BizType   string // 控制台配置的审核策略，留空用默认策略
	Endpoint  string // 默认 tms.tencentcloudapi.com
	Timeout   time.Duration
}

// Result 云审核结论。字段与官方响应一一对应，避免二次封装丢信息。
type Result struct {
	Suggestion string   `json:"Suggestion"` // Block / Review / Pass
	Label      string   `json:"Label"`      // Normal / Porn / Abuse / Ad …
	SubLabel   string   `json:"SubLabel"`
	Score      int      `json:"Score"` // 0-100
	Keywords   []string `json:"Keywords"`
	BizType    string   `json:"BizType"`
}

// Blocked 建议直接处置。
func (r Result) Blocked() bool { return r.Suggestion == "Block" }

// NeedReview 建议人工二次确认。
func (r Result) NeedReview() bool { return r.Suggestion == "Review" }

// Passed 未识别到风险。
func (r Result) Passed() bool { return r.Suggestion == "Pass" }

// Score01 把 0-100 的置信度归一化到 0-1，便于存 moderation_record.score。
func (r Result) Score01() float64 { return float64(r.Score) / 100 }

// Client 云审核客户端，零值不可用，请用 New 构造。
type Client struct {
	cfg  Config
	impl *tc3.Client
}

// New 构造客户端。凭据为空时返回禁用状态的客户端（Enabled() == false）。
func New(cfg Config) *Client {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "tms.tencentcloudapi.com"
	}
	if cfg.Region == "" {
		cfg.Region = "ap-guangzhou"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	if !cfg.Enabled || cfg.SecretID == "" || cfg.SecretKey == "" {
		return &Client{cfg: cfg}
	}
	return &Client{
		cfg:  cfg,
		impl: tc3.New(tc3.Credential{SecretID: cfg.SecretID, SecretKey: cfg.SecretKey}, service, cfg.Endpoint, cfg.Timeout),
	}
}

// Enabled 是否可用：开关打开且凭据齐全。
func (c *Client) Enabled() bool { return c != nil && c.impl != nil }

// Moderate 送审一段文本。
//
// Content 按官方要求做 Base64；DataId 用业务ID便于在控制台按 ID 回溯；
// User 带 UserId 让云端能做账号维度的恶意用户识别（RiskDetails）。
func (c *Client) Moderate(ctx context.Context, text, dataID string, uid int64, nickname string) (*Result, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("tms: 云审核未启用")
	}
	if text == "" {
		return &Result{Suggestion: "Pass", Label: "Normal"}, nil
	}

	payload := map[string]any{
		"Content": base64.StdEncoding.EncodeToString([]byte(text)),
		"DataId":  truncate(dataID, 64),
		"User": map[string]any{
			"UserId":   fmt.Sprint(uid),
			"Nickname": truncate(nickname, 64),
		},
	}
	if c.cfg.BizType != "" {
		payload["BizType"] = c.cfg.BizType
	}

	raw, err := c.impl.Call(ctx, action, version, c.cfg.Region, payload)
	if err != nil {
		return nil, fmt.Errorf("tms: 调用失败: %w", err)
	}
	var out Result
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("tms: 解析响应失败: %w", err)
	}
	if out.Suggestion == "" {
		return nil, fmt.Errorf("tms: 响应缺少 Suggestion 字段")
	}
	return &out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
