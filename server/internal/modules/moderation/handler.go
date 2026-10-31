package moderation

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/datechat/server/internal/middleware"
	"github.com/datechat/server/internal/pkg/resp"
)

// Handler 暴露内容与关系相关接口。
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register 注册用户端路由：举报。
func (h *Handler) Register(v1 *gin.RouterGroup, auth gin.HandlerFunc) {
	g := v1.Group("/reports")
	g.Use(auth)
	g.POST("", h.submit)
}

// RegisterAdmin 注册后台路由：登录、举报处置、审核复审、用户处置。
func (h *Handler) RegisterAdmin(v1 *gin.RouterGroup, adminAuth gin.HandlerFunc) {
	// 登录本身不能要求已登录
	v1.POST("/admin/login", h.adminLogin)

	g := v1.Group("/admin")
	g.Use(adminAuth)
	g.GET("/reports", h.listReports)
	g.POST("/reports/:id/handle", h.handleReport)
	g.GET("/moderations", h.listModerations)
	g.POST("/moderations/:id/review", h.reviewModeration)
	g.POST("/users/:uid/status", h.setUserStatus)
}

type reportReq struct {
	TargetType int      `json:"target_type"`
	TargetID   string   `json:"target_id"`
	TargetUID  int64    `json:"target_uid"`
	Reason     string   `json:"reason"`
	Detail     string   `json:"detail"`
	Evidence   []string `json:"evidence"`
}

// 取值辅助：直接从上下文拿，拿不到返回零值（路由已挂鉴权中间件，正常情况必然有值）。
func userID(c *gin.Context) int64     { return middleware.CurrentUID(c) }
func adminID(c *gin.Context) int64    { return middleware.CurrentAdminID(c) }
func adminName(c *gin.Context) string { return middleware.CurrentAdminName(c) }

func (h *Handler) submit(c *gin.Context) {
	uid := userID(c)
	var req reportReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, err)
		return
	}
	rep, err := h.svc.SubmitReport(c.Request.Context(), uid, ReportInput{
		TargetType: req.TargetType,
		TargetID:   req.TargetID,
		TargetUID:  req.TargetUID,
		Reason:     req.Reason,
		Detail:     req.Detail,
		Evidence:   req.Evidence,
	})
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"id": rep.ID, "status": rep.Status})
}

type adminLoginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *Handler) adminLogin(c *gin.Context) {
	var req adminLoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, err)
		return
	}
	token, expireAt, admin, err := h.svc.AdminLogin(c.Request.Context(), req.Username, req.Password, c.ClientIP())
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{
		"token":     token,
		"expire_at": expireAt.UnixMilli(),
		"admin_id":  admin.ID,
		"role":      admin.Role,
		"real_name": admin.RealName,
	})
}

type handleReq struct {
	Action string `json:"action"` // warn / freeze / ban / ignore
	Note   string `json:"note"`
}

func (h *Handler) handleReport(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	var req handleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, err)
		return
	}
	if err := h.svc.HandleReport(c.Request.Context(), adminID(c), adminName(c), c.ClientIP(), id, req.Action, req.Note); err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"id": id})
}

type reviewReq struct {
	Pass bool   `json:"pass"`
	Note string `json:"note"`
}

func (h *Handler) reviewModeration(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	var req reviewReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, err)
		return
	}
	if err := h.svc.ReviewModeration(c.Request.Context(), adminID(c), id, req.Pass, req.Note); err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"id": id})
}

type statusReq struct {
	Action string `json:"action"` // freeze / ban / unban
	Note   string `json:"note"`
}

func (h *Handler) setUserStatus(c *gin.Context) {
	uid, err := strconv.ParseUint(c.Param("uid"), 10, 64)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	var req statusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, err)
		return
	}
	if err := h.svc.SetUserStatus(c.Request.Context(), adminID(c), adminName(c), c.ClientIP(), int64(uid), req.Action, req.Note); err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"uid": uid})
}

func (h *Handler) listReports(c *gin.Context) {
	status, _ := strconv.Atoi(c.Query("status"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	list, total, err := h.svc.ListReports(c.Request.Context(), status, page, size)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"list": list, "total": total, "page": page, "size": size})
}

func (h *Handler) listModerations(c *gin.Context) {
	result, _ := strconv.Atoi(c.Query("result"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	list, total, err := h.svc.ListModeration(c.Request.Context(), result, page, size)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"list": list, "total": total, "page": page, "size": size})
}
