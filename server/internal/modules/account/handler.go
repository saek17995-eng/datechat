package account

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/datechat/server/internal/middleware"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/resp"
)

// Handler 账号模块的 HTTP 入口。
type Handler struct {
	svc *Service
}

// NewHandler 构造 handler。
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register 挂载账号相关路由。
//
// 公开接口：发码、登录、刷新（刷新必须免登录，否则 access 过期后无法自救）；
// 受保护接口：退出登录、我的信息、重新签发 UserSig。
func (h *Handler) Register(rg *gin.RouterGroup, auth gin.HandlerFunc) {
	rg.POST("/auth/sms/code", h.sendCode)
	rg.POST("/auth/login", h.login)
	rg.POST("/auth/refresh", h.refresh)

	secured := rg.Group("")
	secured.Use(auth)
	secured.POST("/auth/logout", h.logout)
	secured.GET("/auth/me", h.me)
	secured.GET("/im/usersig", h.userSig)
}

type sendCodeReq struct {
	Phone string `json:"phone" binding:"required"`
	Scene string `json:"scene"`
}

type loginReq struct {
	Phone      string `json:"phone" binding:"required"`
	Code       string `json:"code" binding:"required"`
	DeviceID   string `json:"device_id" binding:"required"`
	Platform   int    `json:"platform"`
	AppVersion string `json:"app_version"`
	PushToken  string `json:"push_token"`
}

type refreshReq struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// sendCode 发送登录验证码。
func (h *Handler) sendCode(c *gin.Context) {
	var req sendCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("请填写手机号"))
		return
	}
	out, err := h.svc.SendCode(c.Request.Context(), SendCodeInput{
		Phone: req.Phone,
		Scene: strings.TrimSpace(req.Scene),
		IP:    c.ClientIP(),
	})
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

// login 手机号 + 验证码登录（未注册自动注册）。
func (h *Handler) login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("请填写手机号、验证码与设备标识"))
		return
	}
	out, err := h.svc.Login(c.Request.Context(), LoginInput{
		Phone:      req.Phone,
		Code:       req.Code,
		DeviceID:   req.DeviceID,
		Platform:   req.Platform,
		AppVersion: req.AppVersion,
		PushToken:  req.PushToken,
		IP:         c.ClientIP(),
		UserAgent:  c.Request.UserAgent(),
	})
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

// refresh 用 refresh token 换新的双令牌。
func (h *Handler) refresh(c *gin.Context) {
	var req refreshReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("缺少 refresh_token"))
		return
	}
	out, err := h.svc.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

// logout 退出登录。
func (h *Handler) logout(c *gin.Context) {
	uid := middleware.MustUID(c)
	deviceID := middleware.CurrentDeviceID(c)
	if err := h.svc.Logout(c.Request.Context(), uid, deviceID); err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"ok": true})
}

// me 返回当前登录用户摘要。
func (h *Handler) me(c *gin.Context) {
	uid := middleware.MustUID(c)
	out, err := h.svc.Me(c.Request.Context(), uid)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

// userSig 重新签发 IM UserSig。
func (h *Handler) userSig(c *gin.Context) {
	uid := middleware.MustUID(c)
	out, err := h.svc.UserSig(c.Request.Context(), uid)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}
