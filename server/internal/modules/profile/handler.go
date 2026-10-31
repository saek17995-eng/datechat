package profile

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/datechat/server/internal/middleware"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/resp"
)

// Handler 资料模块的 HTTP 入口。
type Handler struct {
	svc *Service
}

// NewHandler 构造 handler。
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register 挂载资料相关路由。
//
// 路由设计：/users/me/* 处理"我的"，/users/:uid 处理"他人主页"。
// 两个前缀的层级不同（前者比后者多一段），gin 的路由树不会冲突。
func (h *Handler) Register(rg *gin.RouterGroup, auth gin.HandlerFunc) {
	secured := rg.Group("")
	secured.Use(auth)
	{
		secured.GET("/users/me/profile", h.myProfile)
		secured.PUT("/users/me/profile", h.updateProfile)
		secured.POST("/users/me/avatar", h.setAvatar)
		secured.POST("/users/me/heartbeat", h.heartbeat)
		secured.GET("/users/:uid/profile", h.userProfile)
		secured.GET("/users/:uid", h.userProfile) // 兼容简短写法
		secured.POST("/uploads/local", h.uploadLocal)
		secured.POST("/uploads/sign", h.uploadSign)
		secured.GET("/cities", h.cities)
		// P4：拉黑关系
		secured.POST("/users/:uid/block", h.blockUser)
		secured.DELETE("/users/:uid/block", h.unblockUser)
		secured.GET("/users/me/blocks", h.listBlocked)
	}
}

type setAvatarReq struct {
	URL string `json:"url" binding:"required"`
}

type uploadSignReq struct {
	Scene int    `json:"scene"`
	Ext   string `json:"ext"`
}

// myProfile 我的资料。
func (h *Handler) myProfile(c *gin.Context) {
	uid := middleware.MustUID(c)
	out, err := h.svc.MeProfile(c.Request.Context(), uid)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

// updateProfile 局部更新资料。
func (h *Handler) updateProfile(c *gin.Context) {
	uid := middleware.MustUID(c)
	var req ProfileInput
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("请求格式不正确"))
		return
	}
	out, err := h.svc.UpdateProfile(c.Request.Context(), uid, req)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

// setAvatar 提交头像地址（必须来自上传接口）。
func (h *Handler) setAvatar(c *gin.Context) {
	uid := middleware.MustUID(c)
	var req setAvatarReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("缺少头像地址"))
		return
	}
	out, err := h.svc.SetAvatar(c.Request.Context(), uid, req.URL)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

// heartbeat 上报在线心跳。
func (h *Handler) heartbeat(c *gin.Context) {
	uid := middleware.MustUID(c)
	out, err := h.svc.Heartbeat(c.Request.Context(), uid)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

// userProfile 他人主页。
func (h *Handler) userProfile(c *gin.Context) {
	viewer := middleware.MustUID(c)
	raw := strings.TrimSpace(c.Param("uid"))
	uid, perr := strconv.ParseInt(raw, 10, 64)
	if perr != nil || uid <= 0 {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("用户 ID 不合法"))
		return
	}
	out, err := h.svc.PublicProfile(c.Request.Context(), viewer, uid)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

// uploadLocal 本地通道文件上传（multipart/form-data）。
// 字段名 file；scene 用表单字段或查询参数传，默认头像。
func (h *Handler) uploadLocal(c *gin.Context) {
	uid := middleware.MustUID(c)
	file, err := c.FormFile("file")
	if err != nil {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("请选择要上传的图片"))
		return
	}
	scene := parseIntOrDefault(SceneAvatar, c.PostForm("scene"), c.Query("scene"))
	if scene < SceneAvatar || scene > SceneEvidence {
		scene = SceneAvatar
	}
	out, err := h.svc.UploadLocal(c.Request.Context(), uid, scene, file)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.Created(c, out)
}

// uploadSign 获取上传凭证。local 通道返回上传接口，cos 通道返回直传签名。
func (h *Handler) uploadSign(c *gin.Context) {
	uid := middleware.MustUID(c)
	var req uploadSignReq
	// 允许空 body：全部走默认值（头像场景）。
	_ = c.ShouldBindJSON(&req)
	scene := req.Scene
	if scene < SceneAvatar || scene > SceneEvidence {
		scene = SceneAvatar
	}
	out, err := h.svc.UploadSign(c.Request.Context(), uid, scene, strings.TrimPrefix(strings.TrimSpace(req.Ext), "."))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

// cities 城市字典。
func (h *Handler) cities(c *gin.Context) {
	out, err := h.svc.Cities(c.Request.Context())
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

// parseIntOrDefault 依次尝试若干候选值，取第一个能解析的；都不可用则返回默认值。
// 表单字段与查询参数都支持，是为了让 curl 与客户端都能少写一点代码。
func parseIntOrDefault(def int, values ...string) int {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// blockUser 拉黑某人。拉黑后双方都不能再给对方发消息（服务端与 IM 回调双重拦截）。
func (h *Handler) blockUser(c *gin.Context) {
	uid := middleware.MustUID(c)
	target, err := strconv.ParseUint(c.Param("uid"), 10, 64)
	if err != nil {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("用户ID不合法"))
		return
	}
	if int64(target) == uid {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("不能拉黑自己"))
		return
	}
	if err := h.svc.BlockUser(c.Request.Context(), uid, int64(target)); err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"uid": target, "blocked": true})
}

func (h *Handler) unblockUser(c *gin.Context) {
	uid := middleware.MustUID(c)
	target, err := strconv.ParseUint(c.Param("uid"), 10, 64)
	if err != nil {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("用户ID不合法"))
		return
	}
	if err := h.svc.UnblockUser(c.Request.Context(), uid, int64(target)); err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"uid": target, "blocked": false})
}

func (h *Handler) listBlocked(c *gin.Context) {
	uid := middleware.MustUID(c)
	page := parseIntOrDefault(1, c.Query("page"))
	size := parseIntOrDefault(20, c.Query("size"))
	list, total, err := h.svc.ListBlocked(c.Request.Context(), uid, page, size)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"list": list, "total": total, "page": page, "size": size})
}
