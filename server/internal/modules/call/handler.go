package call

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/datechat/server/internal/middleware"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/resp"
)

// Handler 通话接口。
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register 注册用户侧路由。
// 注意 /call/price 单独一组：gin 的路由树不允许在同一层同时出现静态段与通配段，
// 写成 /calls/price 会和 /calls/:call_no 冲突并直接 panic。
func (h *Handler) Register(v1 *gin.RouterGroup, auth gin.HandlerFunc) {
	v1.GET("/call/price", auth, h.price)

	g := v1.Group("/calls")
	g.Use(auth)
	{
		g.POST("", h.create)
		g.GET("", h.list)
		g.GET("/:call_no", h.detail)
		g.POST("/:call_no/accept", h.accept)
		g.POST("/:call_no/reject", h.reject)
		g.POST("/:call_no/cancel", h.cancel)
		g.POST("/:call_no/hangup", h.hangup)
		g.POST("/:call_no/heartbeat", h.heartbeat)
	}
}

// RegisterAdmin 注册后台路由：巡检与通话查询。
func (h *Handler) RegisterAdmin(v1 *gin.RouterGroup, adminAuth gin.HandlerFunc) {
	g := v1.Group("/admin/calls")
	g.Use(adminAuth)
	{
		g.POST("/sweep", h.sweep)
	}
}

type createReq struct {
	CalleeID  int64 `json:"callee_id" binding:"required"`
	MediaType int   `json:"media_type"` // 1语音 2视频，缺省语音
}

func (h *Handler) create(c *gin.Context) {
	var req createReq
	if err := c.ShouldBindJSON(&req); err != nil || req.CalleeID <= 0 {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("缺少被叫用户"))
		return
	}
	if req.MediaType == 0 {
		req.MediaType = MediaAudio
	}
	out, err := h.svc.Create(c.Request.Context(), middleware.MustUID(c), req.CalleeID, req.MediaType)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

func (h *Handler) accept(c *gin.Context) {
	out, err := h.svc.Accept(c.Request.Context(), middleware.MustUID(c), c.Param("call_no"))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

func (h *Handler) reject(c *gin.Context) {
	st, err := h.svc.Reject(c.Request.Context(), middleware.MustUID(c), c.Param("call_no"))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, st)
}

func (h *Handler) cancel(c *gin.Context) {
	st, err := h.svc.Cancel(c.Request.Context(), middleware.MustUID(c), c.Param("call_no"))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, st)
}

func (h *Handler) hangup(c *gin.Context) {
	st, err := h.svc.Hangup(c.Request.Context(), middleware.MustUID(c), c.Param("call_no"))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, st)
}

type beatReq struct {
	ElapsedSec int `json:"elapsed_sec"` // 客户端观测时长，仅用于排查，不参与计费
}

func (h *Handler) heartbeat(c *gin.Context) {
	var req beatReq
	_ = c.ShouldBindJSON(&req)
	out, err := h.svc.Heartbeat(c.Request.Context(), middleware.MustUID(c), c.Param("call_no"), req.ElapsedSec)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

func (h *Handler) detail(c *gin.Context) {
	out, err := h.svc.Detail(c.Request.Context(), middleware.MustUID(c), c.Param("call_no"))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

func (h *Handler) list(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	list, total, err := h.svc.List(c.Request.Context(), middleware.MustUID(c), page, size)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"list": list, "total": total, "page": page, "size": size})
}

func (h *Handler) price(c *gin.Context) {
	uid, _ := strconv.ParseInt(c.Query("user_id"), 10, 64)
	media, _ := strconv.Atoi(c.DefaultQuery("media_type", strconv.Itoa(MediaAudio)))
	p, err := h.svc.Price(c.Request.Context(), uid, media)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{
		"user_id":       uid,
		"media_type":    media,
		"per_min":       p.PerMin,
		"platform_rate": p.PlatformRate,
		"price_mode":    p.Mode,
	})
}

// sweep 手动触发巡检。正常由后台定时任务驱动，这里留给运营/验收即时触发。
func (h *Handler) sweep(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	rep, err := h.svc.Sweep(c.Request.Context(), limit)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, rep)
}
