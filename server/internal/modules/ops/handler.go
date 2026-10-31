package ops

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/datechat/server/internal/middleware"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/resp"
)

// Handler 运营后台接口。
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// RegisterAdmin 注册后台路由。
//
// 全部挂 AdminAuth：用户令牌哪怕有效也不能进后台。
// 路径前缀 /admin 与内容安全模块的处置接口一致，方便网关按前缀做二次鉴权与审计。
func (h *Handler) RegisterAdmin(v1 *gin.RouterGroup, adminAuth gin.HandlerFunc) {
	g := v1.Group("/admin")
	g.Use(adminAuth)
	{
		g.GET("/overview", h.overview)
		g.GET("/users", h.listUsers)
		g.GET("/users/:uid", h.getUser)
		g.POST("/users/:uid/ban", h.banUser)
		g.POST("/users/:uid/unban", h.unbanUser)
		g.GET("/orders", h.listOrders)
		g.GET("/calls", h.listCalls)
		g.GET("/oplogs", h.listOpLogs)
	}
}

func pageOf(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	return page, size
}

// startOfToday 今天零点。概览的"今日"口径统一在这里定义。
func startOfToday(now time.Time) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, now.Location())
}

func (h *Handler) overview(c *gin.Context) {
	out, err := h.svc.Overview(c.Request.Context(), startOfToday(time.Now()))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

func (h *Handler) listUsers(c *gin.Context) {
	page, size := pageOf(c)
	status, _ := strconv.Atoi(c.DefaultQuery("status", "0"))
	list, total, err := h.svc.ListUsers(c.Request.Context(), UserQuery{
		Keyword: c.Query("keyword"),
		Status:  status,
		Page:    page,
		Size:    size,
	})
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"list": list, "total": total, "page": page, "size": size})
}

func (h *Handler) getUser(c *gin.Context) {
	uid, err := strconv.ParseInt(c.Param("uid"), 10, 64)
	if err != nil || uid <= 0 {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("用户 ID 不合法"))
		return
	}
	out, err := h.svc.GetUser(c.Request.Context(), uid)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

type banReq struct {
	Days   int    `json:"days"`   // 0 表示永久
	Reason string `json:"reason"` // 必填：封禁必须能说清为什么
}

func (h *Handler) banUser(c *gin.Context) {
	uid, err := strconv.ParseInt(c.Param("uid"), 10, 64)
	if err != nil || uid <= 0 {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("用户 ID 不合法"))
		return
	}
	var req banReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("请求体不合法"))
		return
	}
	if req.Reason == "" {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("封禁必须填写原因"))
		return
	}
	if req.Days < 0 {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("封禁天数不能为负"))
		return
	}
	if err := h.svc.SetUserStatus(c.Request.Context(),
		middleware.CurrentAdminID(c), middleware.CurrentAdminName(c),
		uid, StatusBanned, req.Days, req.Reason, c.ClientIP()); err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"uid": uid, "status": StatusBanned, "days": req.Days})
}

func (h *Handler) unbanUser(c *gin.Context) {
	uid, err := strconv.ParseInt(c.Param("uid"), 10, 64)
	if err != nil || uid <= 0 {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("用户 ID 不合法"))
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := h.svc.SetUserStatus(c.Request.Context(),
		middleware.CurrentAdminID(c), middleware.CurrentAdminName(c),
		uid, StatusNormal, 0, req.Note, c.ClientIP()); err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"uid": uid, "status": StatusNormal})
}

func (h *Handler) listOrders(c *gin.Context) {
	page, size := pageOf(c)
	// status 默认 -1（不过滤）：订单状态 0 是合法值，不能用 0 当"不限"。
	status, err := strconv.Atoi(c.DefaultQuery("status", "-1"))
	if err != nil {
		status = -1
	}
	userID, _ := strconv.ParseInt(c.DefaultQuery("user_id", "0"), 10, 64)
	list, total, err := h.svc.ListOrders(c.Request.Context(), OrderQuery{
		UserID:  userID,
		Status:  status,
		OrderNo: c.Query("order_no"),
		Page:    page,
		Size:    size,
	})
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"list": list, "total": total, "page": page, "size": size})
}

func (h *Handler) listCalls(c *gin.Context) {
	page, size := pageOf(c)
	status, err := strconv.Atoi(c.DefaultQuery("status", "-1"))
	if err != nil {
		status = -1
	}
	userID, _ := strconv.ParseInt(c.DefaultQuery("user_id", "0"), 10, 64)
	list, total, err := h.svc.ListCalls(c.Request.Context(), CallQuery{
		UserID: userID,
		Status: status,
		Page:   page,
		Size:   size,
	})
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"list": list, "total": total, "page": page, "size": size})
}

func (h *Handler) listOpLogs(c *gin.Context) {
	page, size := pageOf(c)
	adminID, _ := strconv.ParseInt(c.DefaultQuery("admin_id", "0"), 10, 64)
	list, total, err := h.svc.ListOpLogs(c.Request.Context(), OpLogQuery{
		AdminID: adminID,
		Module:  c.Query("module"),
		Page:    page,
		Size:    size,
	})
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"list": list, "total": total, "page": page, "size": size})
}
