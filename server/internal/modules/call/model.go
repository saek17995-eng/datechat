// Package call 实现一对一音视频通话与按分钟计费。
//
// 这个模块的核心不是"打通音视频"（那是 TRTC SDK 的事），而是三件事：
//
//  1. 状态机：ringing → talking → ended 的每一次跃迁都要落事件流水，
//     否则"谁先挂的""为什么断的"在客诉时永远查不清；
//  2. 计费：以服务端时钟为准，按分钟向上取整，预扣 + 心跳续扣 + 挂断结算；
//  3. 对账：每分钟一个 call_segment（uk_session_seq 天然幂等），
//     任何时刻都能回答"这通电话扣了几次、每次扣了多少"。
package call

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/datechat/server/internal/pkg/trtc"
)

// 媒体类型。
const (
	MediaAudio = 1
	MediaVideo = 2
)

// 定价模式。
const (
	PriceGlobal = 1 // 全局默认价
	PriceCustom = 2 // 被叫自定价
)

// 通话状态机。数值与 call_session.status 对齐，改动需同步迁移与文档。
const (
	StatusInit     = 0
	StatusRinging  = 1
	StatusTalking  = 2
	StatusEnded    = 3
	StatusCanceled = 4
	StatusRejected = 5
	StatusTimeout  = 6
	StatusAbnormal = 7
)

// 结束原因。
const (
	EndNormal      = 1
	EndNoBalance   = 2
	EndHeartbeat   = 3
	EndReject      = 4
	EndNetwork     = 5
	EndRisk        = 6
	EndMaxDuration = 7 // 达到单通上限被系统挂断
	EndNoAnswer    = 8 // 振铃超时未被接听
)

// 结束状态集合：进入这些状态后不允许再跃迁。
func isFinal(status int) bool {
	switch status {
	case StatusEnded, StatusCanceled, StatusRejected, StatusTimeout, StatusAbnormal:
		return true
	}
	return false
}

// 分钟切片状态。
const (
	SegmentFrozen  = 1 // 已预扣未结算
	SegmentSettled = 2 // 已结算
	SegmentRefund  = 3 // 已退回
)

// Session 通话会话。json 标签与表列名一致（snake_case），
// 这样详情接口直接返回结构体也不会出现字段命名风格割裂。
type Session struct {
	ID              int64      `json:"id"`
	CallNo          string     `json:"call_no"`
	CallerID        int64      `json:"caller_id"`
	CalleeID        int64      `json:"callee_id"`
	MediaType       int        `json:"media_type"`
	PriceMode       int        `json:"price_mode"`
	PricePerMin     int        `json:"price_per_min"`
	PlatformRate    int        `json:"platform_rate"`
	PayerID         int64      `json:"payer_id"`
	Status          int        `json:"status"`
	RoomID          string     `json:"room_id"`
	ChargedMinutes  int        `json:"charged_minutes"`
	StartedAt       *time.Time `json:"started_at"`
	LastBeatAt      *time.Time `json:"last_beat_at"`
	EndedAt         *time.Time `json:"ended_at"`
	DurationSec     int        `json:"duration_sec"`
	BillableMinutes int        `json:"billable_minutes"`
	TotalCoins      int64      `json:"total_coins"`
	PayeeIncome     int64      `json:"payee_income"`
	PlatformFee     int64      `json:"platform_fee"`
	EndReason       int        `json:"end_reason"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// PeerOf 返回对端用户 ID。
func (s *Session) PeerOf(uid int64) int64 {
	if s == nil {
		return 0
	}
	if s.CallerID == uid {
		return s.CalleeID
	}
	return s.CallerID
}

// IsParticipant 判断用户是否是通话参与方。
func (s *Session) IsParticipant(uid int64) bool {
	return s != nil && (s.CallerID == uid || s.CalleeID == uid)
}

// Segment 分钟切片（预扣与结算的最小单位，天然幂等）。
type Segment struct {
	ID            int64      `json:"id"`
	CallSessionID int64      `json:"call_session_id"`
	SeqNo         int        `json:"seq_no"`
	StartAt       time.Time  `json:"start_at"`
	EndAt         *time.Time `json:"end_at"`
	Seconds       int        `json:"seconds"`
	Coins         int64      `json:"coins"`
	Status        int        `json:"status"`
}

// Price 生效定价：每分钟金币 + 平台抽成‰。
type Price struct {
	PerMin       int64
	PlatformRate int // 千分比，1000 表示平台全收
	Mode         int // PriceGlobal / PriceCustom
}

// Split 按抽成率拆分收入：返回被叫收益与平台抽成。
// 用"平台抽成 = 总额 * rate / 1000"，收益取差额，保证两者相加恒等于总额，不会因取整丢金币。
func (p Price) Split(total int64) (income, fee int64) {
	if p.PlatformRate <= 0 {
		return total, 0
	}
	if p.PlatformRate >= 1000 {
		return 0, total
	}
	fee = total * int64(p.PlatformRate) / 1000
	income = total - fee
	return income, fee
}

// BillableMinutes 计费分钟数：向上取整。
// 计费以服务端时钟为准，客户端上报的时长只用于展示，不参与结算。
func BillableMinutes(seconds int) int {
	if seconds <= 0 {
		return 0
	}
	m := seconds / 60
	if seconds%60 != 0 {
		m++
	}
	return m
}

// Event 状态机事件流水。
type Event struct {
	ID            int64     `json:"id"`
	CallSessionID int64     `json:"call_session_id"`
	Event         string    `json:"event"`
	FromStatus    int       `json:"from_status"`
	ToStatus      int       `json:"to_status"`
	OperatorID    int64     `json:"operator_id"`
	Payload       *string   `json:"payload"`
	CreatedAt     time.Time `json:"created_at"`
}

// RoomTicket 进房凭据，由 trtc 包签发。
// 用类型别名而不是重新定义结构，客户端拿到字段名与 TRTC SDK 需要的一致。
type RoomTicket = trtc.Credential

// RoomProvider 进房凭据能力（TRTC）。
// 定义成接口而不是直接依赖 trtc 包，是为了让通话模块在单测里能塞一个假实现。
type RoomProvider interface {
	Enabled() bool
	SdkAppID() int
	Issue(userID, roomID string) (*RoomTicket, error)
	NewRoomID() string
}

// Charger 计费能力（钱包侧实现）。
// 三个方法各自带幂等键，失败语义明确：余额不足必须返回可识别的错误。
type Charger interface {
	Balance(ctx context.Context, uid int64) (int64, error)
	Charge(ctx context.Context, uid int64, bizID string, coins int64, remark string) (int64, error)
	Income(ctx context.Context, uid int64, bizID string, coins int64, remark string) error
}

// NewCallNo 生成通话单号：C + yyyymmddHHMMSS + 6 位随机。
// 带时间前缀便于按单号粗略排序与人工核对客诉。
func NewCallNo(now time.Time, seed uint32) string {
	return "C" + now.Format("20060102150405") + sprintf6(seed)
}

func sprintf6(v uint32) string {
	const digits = "0123456789"
	out := make([]byte, 6)
	for i := 5; i >= 0; i-- {
		out[i] = digits[v%10]
		v /= 10
	}
	return string(out)
}

func randomUint32() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint32(time.Now().UnixNano())
	}
	return binary.BigEndian.Uint32(b[:])
}

// SegmentBizID 生成切片计费的幂等键：{通话号}#{分钟序号}。
// 它同时是 wallet_transaction.biz_id，所以"重复续扣"在账本层就被挡住了。
func SegmentBizID(callNo string, seq int) string {
	return fmt.Sprintf("%s#%d", callNo, seq)
}
