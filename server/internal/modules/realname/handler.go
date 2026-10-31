package realname

import (
	"github.com/gin-gonic/gin"

	"github.com/datechat/server/internal/middleware"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/resp"
)

// Handler 实名模块 HTTP 入口。
type Handler struct {
	svc *Service
}

// NewHandler 构造 handler。
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register 挂载实名路由（全部需要登录）。
func (h *Handler) Register(rg *gin.RouterGroup, auth gin.HandlerFunc) {
	g := rg.Group("/realname")
	g.Use(auth)
	g.POST("/submit", h.submit)
	g.GET("/status", h.status)
	g.POST("/query", h.query)
}

type submitReq struct {
	RealName string `json:"real_name" binding:"required"`
	IDCard   string `json:"id_card" binding:"required"`
}

type queryReq struct {
	Trace string `json:"trace"`
}

// submit 提交实名信息。
func (h *Handler) submit(c *gin.Context) {
	var req submitReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("请填写真实姓名与身份证号"))
		return
	}
	out, err := h.svc.Submit(c.Request.Context(), middleware.MustUID(c), SubmitInput{
		RealName: req.RealName,
		IDCard:   req.IDCard,
	})
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

// status 查询实名状态。
func (h *Handler) status(c *gin.Context) {
	out, err := h.svc.Status(c.Request.Context(), middleware.MustUID(c))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

// query 客户端完成活体检测后回查结果。
func (h *Handler) query(c *gin.Context) {
	var req queryReq
	// 允许空 body：trace 缺省时使用服务端记录的流水号。
	_ = c.ShouldBindJSON(&req)
	out, err := h.svc.Query(c.Request.Context(), middleware.MustUID(c), req.Trace)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}
