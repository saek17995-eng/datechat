package realtime

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/jwt"
	"github.com/datechat/server/internal/pkg/resp"
)

// Handler 提供 WebSocket 握手入口。
type Handler struct {
	hub      *Hub
	tokens   *jwt.Manager
	tokenTTL time.Duration
}

// NewHandler 构造处理器。tokens 为空时握手一律拒绝——
// 宁可让实时通道整个不可用，也不能放行一条未鉴权的长连接。
func NewHandler(hub *Hub, tokens *jwt.Manager) *Handler {
	return &Handler{hub: hub, tokens: tokens}
}

// Register 挂载路由。
//
// 不挂通用限流中间件：长连接只握手一次，限流对它没有意义，
// 反而会在压测时把正常重连全部打回。握手本身有令牌校验兜底。
func (h *Handler) Register(v1 *gin.RouterGroup) {
	v1.GET("/ws", h.Serve)
}

// Serve 握手。
//
// 令牌三种取法，优先级从高到低：
//  1. Authorization: Bearer <token>（Dart 的 WebSocket 支持自定义头，首选）
//  2. ?token=<token>（排查方便，也兼容不支持自定义头的环境）
//  3. Sec-WebSocket-Protocol（浏览器 WebSocket 唯一能自定义的字段）
//
// 校验失败必须返回 401 而不是先升级再关：升级后状态码固定 101，
// 客户端只能靠关闭帧猜原因，排障时非常痛苦。
func (h *Handler) Serve(c *gin.Context) {
	token := extractToken(c)
	if token == "" || h.tokens == nil {
		resp.Fail(c, errs.ErrUnauthorized.WithMsg("缺少访问令牌"))
		return
	}
	claims, err := h.tokens.Parse(token)
	if err != nil {
		resp.Fail(c, err)
		return
	}
	if claims.Type != jwt.TypeAccess {
		resp.Fail(c, errs.ErrTokenInvalid.WithMsg("令牌类型不正确"))
		return
	}

	conn, err := h.hub.Join(c.Writer, c.Request, claims.UID, claims.DeviceID, nil)
	if err != nil {
		// 握手失败时响应头可能已部分写入，这里只记日志，不重复写响应。
		h.hub.log.Warn("WebSocket 升级失败",
			zap.Int64("uid", claims.UID),
			zap.Error(err),
		)
		return
	}

	conn.sendFrame(Frame{
		Type: TypeHello,
		Seq:  h.hub.seq.Add(1),
		Data: HelloData{
			UID:          claims.UID,
			DeviceID:     claims.DeviceID,
			HeartbeatSec: int((pingPeriod * 2 / time.Second)),
			ServerTime:   time.Now().Unix(),
		},
	})
}

// sendFrame 供握手后立刻下发 hello（走同一条发送队列，保证顺序）。
func (c *Conn) sendFrame(frame Frame) {
	c.hub.sendTo(c, frame)
}

func extractToken(c *gin.Context) string {
	const prefix = "Bearer "
	raw := strings.TrimSpace(c.GetHeader("Authorization"))
	if len(raw) > len(prefix) && strings.EqualFold(raw[:len(prefix)], prefix) {
		return strings.TrimSpace(raw[len(prefix):])
	}
	if v := strings.TrimSpace(c.Query("token")); v != "" {
		return v
	}
	// 浏览器场景：subprotocol 里塞 "access_token,<token>"
	for _, proto := range c.Request.Header["Sec-Websocket-Protocol"] {
		for _, part := range strings.Split(proto, ",") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "access_token.") {
				return strings.TrimPrefix(part, "access_token.")
			}
			if part != "" && !strings.Contains(part, ".") && len(part) > 40 {
				return part
			}
		}
	}
	return ""
}
