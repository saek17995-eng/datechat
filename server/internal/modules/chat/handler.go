package chat

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/datechat/server/internal/middleware"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/resp"
)

// Handler 聊天 HTTP 入口。
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register 挂载需要 JWT 的会话与消息接口。
func (h *Handler) Register(rg *gin.RouterGroup, auth gin.HandlerFunc) {
	secured := rg.Group("")
	secured.Use(auth)
	{
		secured.GET("/im/conversations", h.conversations)
		secured.GET("/im/messages", h.messages)
		secured.POST("/im/messages", h.sendMessage)
		secured.POST("/im/conversations/:peer_uid/read", h.markRead)
		secured.POST("/im/messages/:msg_id/recall", h.recall)
	}
}

// RegisterCallback 把回调直接挂到 engine，绕开 JWT 和通用限流。
// 腾讯云事件前回调只有 2 秒超时，误挂限流会造成重试放大或消息审核失效。
func (h *Handler) RegisterCallback(engine *gin.Engine) {
	engine.POST("/v1/im/callback", h.callback)
}

func (h *Handler) conversations(c *gin.Context) {
	page := parsePositive(c.Query("page"), 1)
	size := parsePositive(c.Query("size"), 20)
	out, err := h.svc.Conversations(c.Request.Context(), middleware.MustUID(c), page, size)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

func (h *Handler) messages(c *gin.Context) {
	peerUID, err := strconv.ParseInt(strings.TrimSpace(c.Query("peer_uid")), 10, 64)
	if err != nil || peerUID <= 0 {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("peer_uid 不合法"))
		return
	}
	beforeID, _ := strconv.ParseInt(strings.TrimSpace(c.Query("before_id")), 10, 64)
	limit := parsePositive(c.Query("limit"), 30)
	out, serviceErr := h.svc.Messages(c.Request.Context(), middleware.MustUID(c), peerUID, beforeID, limit)
	if serviceErr != nil {
		resp.Fail(c, serviceErr)
		return
	}
	resp.OK(c, out)
}

func (h *Handler) sendMessage(c *gin.Context) {
	var req SendMessageInput
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("请求格式不正确"))
		return
	}
	out, err := h.svc.SendText(c.Request.Context(), middleware.MustUID(c), req)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.Created(c, out)
}

func (h *Handler) markRead(c *gin.Context) {
	peerUID, err := strconv.ParseInt(strings.TrimSpace(c.Param("peer_uid")), 10, 64)
	if err != nil || peerUID <= 0 {
		resp.Fail(c, errs.ErrInvalidParam.WithMsg("聊天对象不合法"))
		return
	}
	out, err := h.svc.MarkRead(c.Request.Context(), middleware.MustUID(c), peerUID)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

func (h *Handler) recall(c *gin.Context) {
	out, err := h.svc.Recall(c.Request.Context(), middleware.MustUID(c), c.Param("msg_id"))
	if err != nil {
		resp.Fail(c, err)
		return
	}
	resp.OK(c, out)
}

func (h *Handler) callback(c *gin.Context) {
	// 回调协议要求无论业务是否成功都返回 HTTP 200，错误放在 ErrorCode。
	if err := h.svc.VerifyCallback(c.Query("SdkAppid"), c.Query("Sign"), c.Query("RequestTime")); err != nil {
		c.JSON(http.StatusOK, callbackFail(err.Error()))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil || len(raw) == 0 {
		c.JSON(http.StatusOK, callbackFail("请求体为空或过大"))
		return
	}
	sdkAppID, _ := strconv.ParseInt(c.Query("SdkAppid"), 10, 64)
	out := h.svc.HandleCallback(c.Request.Context(), sdkAppID, c.Query("CallbackCommand"), raw)
	c.JSON(http.StatusOK, out)
}

func parsePositive(raw string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
