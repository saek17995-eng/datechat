// Package system 提供客户端启动时要拿的公共配置：协议链接与最低年龄。
//
// 单独成包而不是塞进账号模块：这些配置与登录无关，
// 用户未登录时也要能拿到（打开 App 第一屏就要弹协议）。
package system

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/datechat/server/internal/config"
	"github.com/datechat/server/internal/pkg/resp"
)

// Handler 公共配置接口。
type Handler struct {
	db  *gorm.DB
	cfg *config.Config
	log *zap.Logger
}

func NewHandler(cfg *config.Config, log *zap.Logger, db *gorm.DB) *Handler {
	return &Handler{cfg: cfg, log: log, db: db}
}

// Register 注册路由。不挂鉴权：未登录也要能看到协议。
func (h *Handler) Register(v1 *gin.RouterGroup) {
	v1.GET("/app/agreements", h.agreements)
}

// Agreements 客户端弹窗需要的协议信息。
type Agreements struct {
	Version     int    `json:"version"`
	PrivacyURL  string `json:"privacy_url"`
	TermsURL    string `json:"terms_url"`
	MinAge      int    `json:"min_age"`
	NeedConsent bool   `json:"need_consent"`
}

func (h *Handler) agreements(c *gin.Context) {
	out := Agreements{MinAge: h.cfg.Compliance.Policy().MinAge}

	// 读库拿最新版：协议更新是运营动作，不应该靠发版。
	// 读失败时回退到配置里的默认值，绝不因为这张表挂了就让 App 打不开。
	var rows []struct {
		Key   string `gorm:"column:cfg_key"`
		Value string `gorm:"column:cfg_value"`
	}
	if err := h.db.WithContext(c.Request.Context()).Raw(
		"SELECT cfg_key, cfg_value FROM app_config WHERE cfg_key IN ('agreement.version','agreement.privacy_url','agreement.terms_url')",
	).Scan(&rows).Error; err != nil {
		h.log.Warn("读取协议配置失败，回退默认值", zap.Error(err))
	}
	for _, r := range rows {
		switch r.Key {
		case "agreement.version":
			out.Version = atoi(unquote(r.Value))
		case "agreement.privacy_url":
			out.PrivacyURL = unquote(r.Value)
		case "agreement.terms_url":
			out.TermsURL = unquote(r.Value)
		}
	}
	// 配了链接才需要弹窗：没配说明还没准备协议，弹一个空链接的框只会让人点了个寂寞。
	out.NeedConsent = out.PrivacyURL != "" || out.TermsURL != ""
	resp.OK(c, out)
}

func unquote(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	return v
}

func atoi(v string) int {
	n := 0
	for _, r := range v {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}
