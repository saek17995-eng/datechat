package wallet

import (
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/datechat/server/internal/middleware"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/resp"
	"github.com/datechat/server/internal/pkg/wxpay"
)

// Handler 钱包相关接口。
type Handler struct {
	svc    *Service
	verify func(body []byte, header http.Header) (*wxpay.Notify, error)
}

// NewHandler 构造 handler。verify 用于校验回调签名，由 main 装配真实实现；
// 未配置支付时传 nil，此时回调接口直接拒绝（不允许绕过验签发货）。
func NewHandler(svc *Service, verify func([]byte, http.Header) (*wxpay.Notify, error)) *Handler {
	return &Handler{svc: svc, verify: verify}
}

// Register 注册用户侧路由。
func (h *Handler) Register(v1 *gin.RouterGroup, auth gin.HandlerFunc) {
	g := v1.Group("/wallet")
	g.Use(auth)
	{
		g.GET("", h.me)
		g.GET("/transactions", h.transactions)
		g.GET("/packages", h.packages)
		g.POST("/recharge", h.createOrder)
		g.POST("/recharge/:order_no/sync", h.syncOrder)
		g.POST("/recharge/:order_no/mock-paid", h.mockPaid)
	}
}

// RegisterCallback 注册支付回调（不挂 JWT，由签名校验保证来源）。
func (h *Handler) RegisterCallback(engine *gin.Engine) {
	engine.POST("/v1/pay/wechat/notify", h.wechatNotify)
	engine.POST("/v1/pay/mock/notify", h.mockNotify)
}

func (h *Handler) me(c *gin.Context) {
	w, err := h.svc.Me(c.Request.Context(), middleware.MustUID(c))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, w)
}

func (h *Handler) transactions(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	list, total, err := h.svc.Transactions(c.Request.Context(), middleware.MustUID(c), page, size)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"list": list, "total": total, "page": page, "size": size})
}

func (h *Handler) packages(c *gin.Context) {
	list, err := h.svc.Packages(c.Request.Context())
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"list": list})
}

type rechargeReq struct {
	PackageID int64 `json:"package_id" binding:"required"`
}

func (h *Handler) createOrder(c *gin.Context) {
	var req rechargeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("缺少充值档位"))
		return
	}
	o, params, err := h.svc.CreateOrder(c.Request.Context(), middleware.MustUID(c), req.PackageID, c.ClientIP())
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{
		"order_no":   o.OrderNo,
		"amount_fen": o.AmountFen,
		"coins":      o.Coins,
		"status":     o.Status,
		"pay_params": params,
	})
}

func (h *Handler) syncOrder(c *gin.Context) {
	o, changed, err := h.svc.SyncOrder(c.Request.Context(), c.Param("order_no"))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"order_no": o.OrderNo, "status": o.Status, "changed": changed})
}

// mockPaid 本地模拟支付成功，仅非生产且开启开关时可用。
func (h *Handler) mockPaid(c *gin.Context) {
	if err := h.svc.MockPaid(c.Request.Context(), c.Param("order_no")); err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"order_no": c.Param("order_no"), "status": OrderPaid})
}

// wechatNotify 微信支付回调。
//
// 必须返回 2xx 给微信，否则微信会持续重试；业务失败也要回 200 + 记录日志，
// 让对账脚本去补单，而不是靠微信无限重试来"修好"数据。
func (h *Handler) wechatNotify(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		resp.Fail(c, errs.ErrInvalidParam)
		return
	}
	if h.verify == nil {
		// 没有验签能力就绝不能发货：否则任何人 POST 一下就能给自己加币。
		c.JSON(http.StatusForbidden, gin.H{"code": 10103, "msg": "回调验签未配置"})
		return
	}
	n, err := h.verify(body, c.Request.Header)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 10100, "msg": "回调验签失败"})
		return
	}
	if err := h.svc.HandleNotify(c.Request.Context(), n, string(body)); err != nil {
		// 业务失败仍回 200：微信重试解决不了数据问题，交给对账。
		// 微信的规范是：回包里用 code/message 两个字段表达失败。
		bizErr := errs.From(err)
		c.JSON(http.StatusOK, gin.H{"code": bizErr.Code, "message": bizErr.Msg})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "success"})
}

// mockNotify 本地模拟回调（不验签），仅用于验收与联调。
// 生产环境不会注册该路由（由 main 按 env 决定）。
func (h *Handler) mockNotify(c *gin.Context) {
	var req struct {
		OrderNo string `json:"order_no"`
		TradeNo string `json:"trade_no"`
		Amount  int    `json:"amount_fen"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.OrderNo == "" {
		resp.Fail(c, errs.ErrInvalidParam)
		return
	}
	if req.TradeNo == "" {
		req.TradeNo = "MOCK" + req.OrderNo
	}
	if err := h.svc.HandleNotify(c.Request.Context(), &wxpay.Notify{
		OrderNo: req.OrderNo, TradeNo: req.TradeNo, AmountFen: req.Amount,
		EventType: "MOCK.SUCCESS",
	}, ""); err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, gin.H{"order_no": req.OrderNo})
}
