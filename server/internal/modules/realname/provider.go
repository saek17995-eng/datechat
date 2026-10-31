package realname

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/datechat/server/internal/config"
	"github.com/datechat/server/internal/pkg/crypto"
	"github.com/datechat/server/internal/pkg/tc3"
)

// StartInput 发起一次实名核身。
type StartInput struct {
	UserID   int64
	RealName string
	IDCard   string
}

// Result 核身结论。
type Result struct {
	Passed bool
	Trace  string
	Reason string
}

// StartOutput 发起核身的结果。
//
// 两种形态：
//   - Result 非空：通道能同步判定（本地 mock、人工审核），可直接更新状态；
//   - Result 为空：需要用户完成活体检测，客户端拿 H5URL 去跳转，之后用 Trace 回查。
type StartOutput struct {
	Trace  string
	H5URL  string
	Result *Result
}

// Provider 实名核身通道。
// 抽象出来是为了让 mock / 腾讯云 / 阿里云 / 人工审核能互换，业务状态机不感知通道差异。
type Provider interface {
	// Name 返回通道名，用于日志与 channel 字段映射。
	Name() string
	// Start 发起核身。
	Start(ctx context.Context, in StartInput) (*StartOutput, error)
	// Query 用 Trace 查询核身结果。
	Query(ctx context.Context, trace string) (*Result, error)
}

// —— 本地模拟通道 ——

// MockProvider 本地模拟：提交即通过，用于开发与自动化测试。
// 生产环境必须切到真实通道，否则实名形同虚设。
type MockProvider struct{}

// Name 实现 Provider。
func (MockProvider) Name() string { return "mock" }

// Start 实现 Provider：直接返回通过结论。
func (MockProvider) Start(_ context.Context, in StartInput) (*StartOutput, error) {
	trace, err := crypto.RandomToken(8)
	if err != nil {
		return nil, err
	}
	trace = "mock-" + trace
	return &StartOutput{
		Trace:  trace,
		Result: &Result{Passed: true, Trace: trace},
	}, nil
}

// Query 实现 Provider。
func (MockProvider) Query(_ context.Context, trace string) (*Result, error) {
	if trace == "" {
		return nil, errors.New("realname: 缺少核身流水号")
	}
	return &Result{Passed: true, Trace: trace}, nil
}

// —— 腾讯云人脸核身 ——

// TencentProvider 腾讯云人脸核身（FaceID）。
//
// 流程：DetectAuth 获取 BizToken 与 H5 地址 → 用户完成活体检测 →
// GetDetectInfoEnhanced 用 BizToken 回查比对结果。
type TencentProvider struct {
	client      *tc3.Client
	ruleID      string
	redirectURL string
	region      string
}

// NewTencentProvider 构造腾讯云核身通道。
func NewTencentProvider(cfg config.TencentRealnameConfig, timeout time.Duration) (*TencentProvider, error) {
	cred := tc3.Credential{SecretID: cfg.SecretID, SecretKey: cfg.SecretKey}
	if !cred.Valid() {
		return nil, errors.New("realname: 腾讯云核身缺少 secret_id / secret_key")
	}
	host := "faceid.tencentcloudapi.com"
	if cfg.Endpoint != "" {
		// 配置里给的是完整 URL，这里只取 host 部分。
		host = trimScheme(cfg.Endpoint)
	}
	return &TencentProvider{
		client:      tc3.New(cred, "faceid", host, timeout),
		ruleID:      cfg.RuleID,
		redirectURL: cfg.RedirectURL,
		region:      cfg.Region,
	}, nil
}

// Name 实现 Provider。
func (p *TencentProvider) Name() string { return "tencent" }

// Start 实现 Provider。
func (p *TencentProvider) Start(ctx context.Context, in StartInput) (*StartOutput, error) {
	payload := map[string]any{
		"RuleId": p.ruleID,
		"IdCard": in.IDCard,
		"Name":   in.RealName,
		"Source": "1", // 1=移动端 H5
	}
	if p.redirectURL != "" {
		payload["RedirectUrl"] = p.redirectURL
	}

	raw, err := p.client.Call(ctx, "DetectAuth", "2018-03-01", p.region, payload)
	if err != nil {
		return nil, err
	}
	var resp struct {
		BizToken string `json:"BizToken"`
		URL      string `json:"Url"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("realname: 解析 DetectAuth 响应失败: %w", err)
	}
	if resp.BizToken == "" {
		return nil, errors.New("realname: DetectAuth 未返回 BizToken")
	}
	return &StartOutput{Trace: resp.BizToken, H5URL: resp.URL}, nil
}

// Query 实现 Provider。
func (p *TencentProvider) Query(ctx context.Context, trace string) (*Result, error) {
	payload := map[string]any{
		"BizToken": trace,
		"RuleId":   p.ruleID,
	}
	raw, err := p.client.Call(ctx, "GetDetectInfoEnhanced", "2018-03-01", p.region, payload)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Text struct {
			ErrCode string `json:"ErrCode"`
			ErrMsg  string `json:"ErrMsg"`
		} `json:"Text"`
		IdCard string `json:"IdCard"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("realname: 解析 GetDetectInfoEnhanced 响应失败: %w", err)
	}

	// IdCard 为该次核身中"身份证比对"的结论，0 表示一致；
	// Text.ErrCode 为活体检测结论，0 表示通过。两者缺一不可。
	passed := resp.IdCard == "0" && resp.Text.ErrCode == "0"
	reason := ""
	if !passed {
		reason = fmt.Sprintf("核身未通过（身份证=%s，活体=%s %s）", resp.IdCard, resp.Text.ErrCode, resp.Text.ErrMsg)
	}
	return &Result{Passed: passed, Trace: trace, Reason: reason}, nil
}

// trimScheme 去掉协议前缀，签名用的 host 不能带 scheme。
func trimScheme(endpoint string) string {
	endpoint = strings.TrimPrefix(endpoint, "https://")
	return strings.TrimPrefix(endpoint, "http://")
}
