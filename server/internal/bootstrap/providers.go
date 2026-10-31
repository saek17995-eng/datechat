package bootstrap

import (
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/datechat/server/internal/config"
	"github.com/datechat/server/internal/modules/realname"
	"github.com/datechat/server/internal/pkg/sms"
	"github.com/datechat/server/internal/pkg/tc3"
)

// NewSMSProvider 按配置构造短信通道。
// 未知通道直接报错而不是静默降级：短信发不出去属于"看起来正常但用户收不到码"的隐性故障。
func NewSMSProvider(cfg *config.Config, log *zap.Logger) (sms.Provider, error) {
	switch cfg.SMS.Provider {
	case "tencent":
		provider, err := sms.NewTencentProvider(sms.TencentConfig{
			Cred:       tc3.Credential{SecretID: cfg.SMS.Tencent.SecretID, SecretKey: cfg.SMS.Tencent.SecretKey},
			SdkAppID:   cfg.SMS.Tencent.SdkAppID,
			SignName:   cfg.SMS.Tencent.SignName,
			TemplateID: cfg.SMS.Tencent.TemplateID,
			Region:     cfg.SMS.Tencent.Region,
			Endpoint:   "sms.tencentcloudapi.com",
		}, 5*time.Second)
		if err != nil {
			return nil, fmt.Errorf("初始化腾讯云短信失败: %w", err)
		}
		return provider, nil
	case "mock", "log", "":
		log.Info("短信通道为本地日志模式，验证码不会真实下发")
		return sms.NewLogProvider(log), nil
	default:
		return nil, fmt.Errorf("未知的短信通道: %q（可选 mock / tencent）", cfg.SMS.Provider)
	}
}

// NewRealnameProvider 按配置构造实名通道。
func NewRealnameProvider(cfg *config.Config, log *zap.Logger) (realname.Provider, error) {
	if !cfg.Realname.Enabled {
		log.Warn("实名认证已关闭，提交接口会直接返回不可用")
		return nil, nil
	}
	switch cfg.Realname.Provider {
	case "tencent":
		provider, err := realname.NewTencentProvider(cfg.Realname.Tencent, 8*time.Second)
		if err != nil {
			return nil, fmt.Errorf("初始化腾讯云人脸核身失败: %w", err)
		}
		return provider, nil
	case "mock", "":
		if cfg.IsProd() {
			log.Warn("实名通道为 mock，生产环境不会真正核验身份，请尽快切换真实通道")
		}
		return realname.MockProvider{}, nil
	default:
		return nil, fmt.Errorf("未知的实名通道: %q（可选 mock / tencent）", cfg.Realname.Provider)
	}
}
