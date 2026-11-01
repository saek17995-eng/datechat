package realtime

// 帧协议：客户端与服务端之间只传这一种信封，业务差异放在 type 与 data 里。
//
// 之所以不按业务拆多个结构体：WebSocket 是长连接，协议演进时客户端版本不齐，
// 一个"未知 type 就忽略"的信封比一堆强类型消息更容易向前兼容。
//
// 客户端 → 服务端：
//
//	{"type":"ping"}                       心跳，服务端回 pong
//	{"type":"signal","to":uid,"call_no":"","kind":"offer|answer|candidate|hangup|busy",
//	 "payload":{}}                        转发给指定用户（WebRTC 协商与通话控制）
//
// 服务端 → 客户端：
//
//	{"type":"hello","data":{"uid":123}}    连接建立
//	{"type":"pong"}                        心跳应答
//	{"type":"new_message","data":{...}}    收到新消息（含发送者昵称与预览）
//	{"type":"message_recall","data":{...}}  对方撤回了一条消息
//	{"type":"message_read","data":{...}}    对方已读
//	{"type":"call_invite","data":{...}}    来电
//	{"type":"call_update","data":{...}}    通话状态变化（接听/拒绝/挂断/取消）
//	{"type":"signal","data":{"from":uid,"call_no":"","kind":"","payload":{}}}
const (
	TypeHello      = "hello"
	TypePing       = "ping"
	TypePong       = "pong"
	TypeNewMessage = "new_message"
	TypeCallInvite = "call_invite"
	TypeCallUpdate = "call_update"
	TypeSignal     = "signal"
	TypeRecall     = "message_recall"
	TypeRead       = "message_read"
	TypeUnread     = "unread"
	TypeError      = "error"
)

// Frame 是唯一的传输信封。
type Frame struct {
	Type string `json:"type"`
	// Seq 由服务端单调递增下发，客户端可据此判断是否漏帧（漏了就全量拉一次）。
	Seq int64 `json:"seq,omitempty"`
	// To 仅用于客户端上行 signal：指定转发目标。
	To int64 `json:"to,omitempty"`
	// CallNo 与 Kind 仅 signal 使用。
	CallNo  string         `json:"call_no,omitempty"`
	Kind    string         `json:"kind,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
	Data    any            `json:"data,omitempty"`
	Msg     string         `json:"msg,omitempty"`
}

// HelloData 连接建立时下发的自身信息。
type HelloData struct {
	UID      int64  `json:"uid"`
	DeviceID string `json:"device_id,omitempty"`
	// HeartbeatSec 建议的客户端心跳间隔。
	HeartbeatSec int `json:"heartbeat_sec"`
	// ServerTime  unix 秒，便于客户端对时。
	ServerTime int64 `json:"server_time"`
}

// UnreadData 未读总数，客户端拿去渲染红点。
type UnreadData struct {
	Total int `json:"total"`
}
