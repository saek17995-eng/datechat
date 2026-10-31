// Package sms 抽象短信验证码下发通道。
//
// 两个实现：
//   - Log：只写日志不发短信，本地开发与自动化测试用；
//   - Tencent：腾讯云短信（SendSms，API 2021-01-11）。
//
// 业务侧只依赖 Provider 接口，切换通道改配置即可，不用动代码。
package sms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/datechat/server/internal/pkg/tc3"
)

// Message 是一条待发送的验证码短信。
type Message struct {
	Phone string        // 11 位手机号，不含 +86
	Code  string        // 验证码
	TTL   time.Duration // 有效期，用于模板里的"X 分钟内有效"
}

// Provider 短信通道。
// 返回 requestID 便于在服务商控制台对账；发送失败必须返回 error，由上层决定是否重试。
type Provider interface {
	Name() string
	Send(ctx context.Context, msg Message) (requestID string, err error)
}

// —— 本地日志通道 ——

// LogProvider 把验证码打进日志，不产生真实短信费用。
type LogProvider struct {
	log *zap.Logger
}

// NewLogProvider 创建日志通道。
func NewLogProvider(log *zap.Logger) *LogProvider {
	return &LogProvider{log: log}
}

// Name 实现 Provider。
func (p *LogProvider) Name() string { return "log" }

// Send 实现 Provider。
func (p *LogProvider) Send(_ context.Context, msg Message) (string, error) {
	if p.log != nil {
		p.log.Info("【本地短信通道】验证码未真实发送",
			zap.String("phone", maskPhone(msg.Phone)),
			zap.String("code", msg.Code),
			zap.Duration("ttl", msg.TTL),
		)
	}
	return "local", nil
}

// —— 腾讯云短信通道 ——

// TencentConfig 腾讯云短信配置。
type TencentConfig struct {
	Cred       tc3.Credential
	SdkAppID   string
	SignName   string
	TemplateID string
	Region     string
	Endpoint   string
}

// TencentProvider 腾讯云短信。
type TencentProvider struct {
	cfg    TencentConfig
	client *tc3.Client
}

// NewTencentProvider 创建腾讯云短信通道。
func NewTencentProvider(cfg TencentConfig, timeout time.Duration) (*TencentProvider, error) {
	if !cfg.Cred.Valid() {
		return nil, errors.New("sms: 腾讯云短信缺少 secret_id / secret_key")
	}
	if cfg.SdkAppID == "" || cfg.SignName == "" || cfg.TemplateID == "" {
		return nil, errors.New("sms: 腾讯云短信缺少 sdk_app_id / sign_name / template_id")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = "sms.tencentcloudapi.com"
	}
	return &TencentProvider{cfg: cfg, client: tc3.New(cfg.Cred, "sms", cfg.Endpoint, timeout)}, nil
}

// Name 实现 Provider。
func (p *TencentProvider) Name() string { return "tencent" }

// sendStatus 是 SendSms 返回的单条发送状态。
type sendStatus struct {
	SerialNo  string `json:"SerialNo"`
	PhoneNum  string `json:"PhoneNumber"`
	Fee       int    `json:"Fee"`
	SessionNo string `json:"SessionContext"`
	Code      string `json:"Code"`
	Message   string `json:"Message"`
}

// Send 实现 Provider。
// 模板约定两个变量：{1} 验证码、{2} 有效分钟数，与 0002_seed 里的模板保持一致。
func (p *TencentProvider) Send(ctx context.Context, msg Message) (string, error) {
	minutes := int(msg.TTL.Minutes())
	if minutes <= 0 {
		minutes = 5
	}
	payload := map[string]any{
		"SmsSdkAppId":      p.cfg.SdkAppID,
		"SignName":         p.cfg.SignName,
		"TemplateId":       p.cfg.TemplateID,
		"PhoneNumberSet":   []string{"+86" + msg.Phone},
		"TemplateParamSet": []string{msg.Code, strconv.Itoa(minutes)},
	}

	raw, err := p.client.Call(ctx, "SendSms", "2021-01-11", p.cfg.Region, payload)
	if err != nil {
		return "", err
	}

	var resp struct {
		SendStatusSet []sendStatus `json:"SendStatusSet"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("sms: 解析 SendSms 响应失败: %w", err)
	}
	if len(resp.SendStatusSet) == 0 {
		return "", errors.New("sms: SendSms 未返回发送状态")
	}
	st := resp.SendStatusSet[0]
	if st.Code != "Ok" {
		return st.SerialNo, fmt.Errorf("sms: 发送失败 %s: %s", st.Code, st.Message)
	}
	return st.SerialNo, nil
}

// maskPhone 日志脱敏，避免日志成为手机号泄露源。
func maskPhone(p string) string {
	if len(p) != 11 {
		return "***"
	}
	return p[:3] + "****" + p[7:]
}
