// Package realtime 提供 WebSocket 长连接通道。
//
// 为什么需要它：P3 的收发消息、P6 的呼叫信令原本都是"客户端主动拉"——
// 消息只有在打开聊天页时才拉得到，被叫方根本不知道有人来电，
// 这在真机上就表现为"对方发了消息我没反应""点了通话对方没响铃"。
//
// 服务端在这里只做三件事，绝不掺和业务规则：
//  1. 鉴权后维护 uid → 连接的映射（一人多设备）；
//  2. 转发：把 A 的 signal 帧原样转给 B（WebRTC 的 offer/answer/ICE 都走这条）；
//  3. 推送：业务模块（聊天/通话）调用 Push 把事件推给目标用户。
//
// 业务模块只依赖一个函数，不 import 本包，避免循环依赖也方便单测塞假实现。
package realtime

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const (
	// 心跳：服务端每 25s 发一次 ping，客户端 60s 内没收到任何帧就重连。
	// 取 25s 是为了在常见 NAT/运营商 60s 空闲回收之前先把连接保活住。
	pingPeriod = 25 * time.Second
	// 写超时略大于 ping 周期，避免正常心跳被自己超时打断。
	writeWait = 30 * time.Second
	// 读超时按"客户端 60s 发一次 ping"给 1.5 倍余量。
	readWait = 90 * time.Second
	// 单帧上限：信令里可能带 SDP（几 KB），512KB 足够且能挡住畸形大包。
	maxMessageSize = 512 * 1024
	// 发送缓冲：单连接堆积超过 64 帧说明对端消费异常，丢弃并记日志，
	// 绝不让慢连接把业务 goroutine 拖死。
	sendBuffer = 64
)

// Hub 管理全部在线连接。零值不可用，用 NewHub 构造。
type Hub struct {
	log *zap.Logger

	mu    sync.RWMutex
	conns map[int64]map[*Conn]struct{}

	seq atomic.Int64

	upgrader websocket.Upgrader
}

// NewHub 构造连接中心。
// checkOrigin 暴露出来是因为浏览器直连时会有 Origin 头，而 App 直连没有；
// 默认放行所有来源——真正的防线是令牌，而不是 Origin（Origin 可伪造）。
func NewHub(log *zap.Logger) *Hub {
	if log == nil {
		log = zap.NewNop()
	}
	return &Hub{
		log:   log,
		conns: make(map[int64]map[*Conn]struct{}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}
}

// Conn 是一条已鉴权的连接。
type Conn struct {
	hub      *Hub
	uid      int64
	deviceID string
	ws       *websocket.Conn
	send     chan []byte
	closeMu  sync.Mutex
	closed   bool
}

// Join 把一条已升级的连接登记到中心并开始读写泵。
// uid 与 deviceID 由调用方完成鉴权后传入——本包不再关心令牌怎么来的。
func (h *Hub) Join(w http.ResponseWriter, r *http.Request, uid int64, deviceID string, header http.Header) (*Conn, error) {
	ws, err := h.upgrader.Upgrade(w, r, header)
	if err != nil {
		return nil, err
	}
	c := &Conn{
		hub:      h,
		uid:      uid,
		deviceID: deviceID,
		ws:       ws,
		send:     make(chan []byte, sendBuffer),
	}

	h.mu.Lock()
	if h.conns[uid] == nil {
		h.conns[uid] = make(map[*Conn]struct{})
	}
	h.conns[uid][c] = struct{}{}
	h.mu.Unlock()

	ws.SetReadLimit(maxMessageSize)
	_ = ws.SetReadDeadline(time.Now().Add(readWait))

	h.log.Info("实时连接建立",
		zap.Int64("uid", uid),
		zap.String("device_id", deviceID),
		zap.String("remote", ws.RemoteAddr().String()),
	)

	go c.writePump()
	go c.readPump()
	return c, nil
}

// Leave 注销连接并关闭底层套接字。可重复调用。
func (h *Hub) Leave(c *Conn) {
	if c == nil {
		return
	}
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return
	}
	c.closed = true
	c.closeMu.Unlock()

	h.mu.Lock()
	if set, ok := h.conns[c.uid]; ok {
		delete(set, c)
		if len(set) == 0 {
			delete(h.conns, c.uid)
		}
	}
	total := len(h.conns)
	h.mu.Unlock()

	_ = c.ws.Close()
	h.log.Info("实时连接断开",
		zap.Int64("uid", c.uid),
		zap.String("device_id", c.deviceID),
		zap.Int("在线用户数", total),
	)
}

// Online 判断用户是否至少有一条在线连接。
// 只用于展示，不做权限判断——连接状态天然有竞态，不能当真相用。
func (h *Hub) Online(uid int64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns[uid]) > 0
}

// Connections 返回当前在线用户数与连接总数，供健康检查与运营台观测。
func (h *Hub) Connections() (users int, conns int) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	users = len(h.conns)
	for _, set := range h.conns {
		conns += len(set)
	}
	return users, conns
}

// Close 关闭全部连接（进程退出前调用，避免客户端等超时才发现断线）。
func (h *Hub) Close() {
	h.mu.RLock()
	all := make([]*Conn, 0, 16)
	for _, set := range h.conns {
		for c := range set {
			all = append(all, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range all {
		h.Leave(c)
	}
}

// Push 给某个用户的所有连接下发一帧。
// 这是业务模块唯一需要调用的方法：消息、来电、通话状态都走它。
// 不返回错误——推送失败不该让"发消息"这种主流程回滚（消息已落库），
// 丢帧由客户端的重连+全量拉取兜底。
func (h *Hub) Push(uid int64, typ string, data any) {
	if uid <= 0 {
		return
	}
	h.push(uid, Frame{Type: typ, Seq: h.seq.Add(1), Data: data})
}

func (h *Hub) push(uid int64, frame Frame) {
	body, err := json.Marshal(frame)
	if err != nil {
		h.log.Error("实时帧序列化失败", zap.String("type", frame.Type), zap.Error(err))
		return
	}

	h.mu.RLock()
	set := h.conns[uid]
	targets := make([]*Conn, 0, len(set))
	for c := range set {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	if len(targets) == 0 {
		// 没在线不是错误：接收方离线时消息已经在库里，下次上线拉会话列表就有了。
		return
	}

	for _, c := range targets {
		select {
		case c.send <- body:
		default:
			// 慢连接：宁可丢这一帧也不能让发送方阻塞。
			h.log.Warn("实时发送缓冲已满，丢弃一帧",
				zap.Int64("uid", uid),
				zap.String("type", frame.Type),
				zap.String("device_id", c.deviceID),
			)
		}
	}
}

// forward 把 A 的 signal 帧原样转给 B，并补上 from / seq。
func (h *Hub) forward(from *Conn, in Frame) {
	if in.To <= 0 {
		h.sendTo(from, Frame{Type: TypeError, Msg: "signal 缺少 to 字段"})
		return
	}
	if in.To == from.uid {
		h.sendTo(from, Frame{Type: TypeError, Msg: "不能转发给自己"})
		return
	}
	out := Frame{
		Type:    TypeSignal,
		Seq:     h.seq.Add(1),
		CallNo:  in.CallNo,
		Kind:    in.Kind,
		Payload: in.Payload,
		Data: map[string]any{
			"from":   from.uid,
			"kind":   in.Kind,
			"callNo": in.CallNo,
		},
	}
	h.push(in.To, out)
}

func (h *Hub) sendTo(c *Conn, frame Frame) {
	body, err := json.Marshal(frame)
	if err != nil {
		return
	}
	select {
	case c.send <- body:
	default:
	}
}

// ---------------------------------------------------------------- 读写泵

func (c *Conn) readPump() {
	defer c.hub.Leave(c)

	for {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				c.hub.log.Debug("实时连接读异常", zap.Int64("uid", c.uid), zap.Error(err))
			}
			return
		}
		_ = c.ws.SetReadDeadline(time.Now().Add(readWait))

		var frame Frame
		if err := json.Unmarshal(data, &frame); err != nil {
			c.hub.sendTo(c, Frame{Type: TypeError, Msg: "帧格式不合法"})
			continue
		}
		switch frame.Type {
		case TypePing:
			c.hub.sendTo(c, Frame{Type: TypePong, Seq: c.hub.seq.Add(1)})
		case TypeSignal:
			c.hub.forward(c, frame)
		default:
			// 未知类型忽略而不是断开：客户端版本比服务端低时会有新帧类型。
			c.hub.log.Debug("忽略未知实时帧", zap.String("type", frame.Type), zap.Int64("uid", c.uid))
		}
	}
}

func (c *Conn) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.hub.Leave(c)
	}()

	for {
		select {
		case body, ok := <-c.send:
			if !ok {
				return
			}
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.ws.WriteMessage(websocket.TextMessage, body); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			// WebSocket 协议级 ping：比应用层心跳更早发现死连接。
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
