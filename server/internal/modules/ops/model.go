// Package ops 运营后台：看数、查人、处置、查订单与通话、审计日志。
//
// 这个包只读业务表 + 写封禁与审计，不承载任何业务逻辑判断：
// 后台能查到的东西比客户端多得多，一旦被人拿到令牌影响面也大，
// 所以所有写操作都必须落 admin_op_log（谁、什么时候、把什么改成了什么）。
package ops

import "time"

// Overview 运营概览。
//
// 全部用"今日/累计"两个口径：今日看异常波动，累计看盘子大小。
// 不做同比环比——那属于 BI 的活，放在接口里只会让查询变慢。
type Overview struct {
	GeneratedAt time.Time `json:"generated_at"`

	UsersTotal    int64 `json:"users_total"`
	UsersNewToday int64 `json:"users_new_today"`
	UsersBanned   int64 `json:"users_banned"`

	OrdersPaidToday    int64 `json:"orders_paid_today"`
	RechargeFenToday   int64 `json:"recharge_fen_today"`
	RechargeCoinsToday int64 `json:"recharge_coins_today"`

	CallsToday     int64 `json:"calls_today"`
	CallMinutes    int64 `json:"call_minutes_today"`
	CallCoinsToday int64 `json:"call_coins_today"`

	ReportsPending    int64 `json:"reports_pending"`
	ModerationPending int64 `json:"moderation_pending"`
	RealnamePending   int64 `json:"realname_pending"`
}

// UserRow 用户列表行。
// 字段名要能对上 SQL 里的列别名：gorm 的默认命名策略把 UID 映射成 uid，
// 而查询里选的是 id，不加 column 标签会静默填成 0（表现为"用户不存在"）。
type UserRow struct {
	UID            int64      `json:"uid" gorm:"column:id"`
	Nickname       string     `json:"nickname"`
	Phone          string     `json:"phone"` // 脱敏后（138****1234）
	Gender         int        `json:"gender"`
	Age            int        `json:"age"`
	CityName       string     `json:"city_name"`
	Status         int        `json:"status"`
	StatusText     string     `json:"status_text"`
	RealnameStatus int        `json:"realname_status"`
	BanUntil       *time.Time `json:"ban_until"`
	BanReason      string     `json:"ban_reason"`
	CoinBalance    int64      `json:"coin_balance"`
	TotalRecharge  int64      `json:"total_recharge"`
	TotalConsume   int64      `json:"total_consume"`
	RegisterIP     string     `json:"register_ip"`
	LastActiveAt   *time.Time `json:"last_active_at"`
	CreatedAt      time.Time  `json:"created_at"`
	ReportCount    int64      `json:"report_count"` // 被举报次数
	Online         bool       `json:"online"`

	// 密文与生日只用于内部换算（脱敏手机号、年龄），绝不出现在响应里。
	PhoneCipher []byte     `json:"-"`
	Birthday    *time.Time `json:"-"`
}

// UserDetail 用户详情：账号 + 资料 + 钱包 + 统计。
type UserDetail struct {
	UserRow
	BlockedCount   int64       `json:"blocked_count"`    // 主动拉黑人数
	BlockedByCount int64       `json:"blocked_by_count"` // 被拉黑次数
	CallCount      int64       `json:"call_count"`
	CallSeconds    int64       `json:"call_seconds"`
	IncomeCoins    int64       `json:"income_coins"`
	RecentReports  []ReportRow `json:"recent_reports"`
	RecentOrders   []OrderRow  `json:"recent_orders"`
}

// ReportRow 举报摘要（后台列表用）。
type ReportRow struct {
	ID        int64     `json:"id"`
	Reason    string    `json:"reason"`
	Status    int       `json:"status"`
	Result    string    `json:"handle_result"`
	CreatedAt time.Time `json:"created_at"`
}

// OrderRow 充值订单。
type OrderRow struct {
	ID         int64      `json:"id"`
	OrderNo    string     `json:"order_no"`
	UserID     int64      `json:"user_id"`
	Nickname   string     `json:"nickname"`
	AmountFen  int64      `json:"amount_fen"`
	Coins      int64      `json:"coins"`
	Channel    string     `json:"channel"`
	Status     int        `json:"status"`
	StatusText string     `json:"status_text"`
	PaidAt     *time.Time `json:"paid_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// CallRow 通话记录。
type CallRow struct {
	ID              int64      `json:"id"`
	CallNo          string     `json:"call_no"`
	CallerID        int64      `json:"caller_id"`
	CalleeID        int64      `json:"callee_id"`
	MediaType       int        `json:"media_type"`
	Status          int        `json:"status"`
	StatusText      string     `json:"status_text"`
	DurationSec     int        `json:"duration_sec"`
	BillableMinutes int        `json:"billable_minutes"`
	TotalCoins      int64      `json:"total_coins"`
	PayeeIncome     int64      `json:"payee_income"`
	PlatformFee     int64      `json:"platform_fee"`
	EndReason       int        `json:"end_reason"`
	EndReasonText   string     `json:"end_reason_text"`
	StartedAt       *time.Time `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

// OpLogRow 后台操作日志。
type OpLogRow struct {
	ID         int64     `json:"id"`
	AdminID    int64     `json:"admin_id"`
	AdminName  string    `json:"admin_name"`
	Module     string    `json:"module"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	Note       string    `json:"note"`
	CreatedAt  time.Time `json:"created_at"`
}

// UserQuery 用户列表筛选。
type UserQuery struct {
	Keyword string // UID / 昵称 / 手机号（完整号走 HMAC）
	Status  int
	Page    int
	Size    int
}

// OrderQuery 订单筛选。
type OrderQuery struct {
	UserID  int64
	Status  int
	OrderNo string
	Page    int
	Size    int
}

// CallQuery 通话筛选。
type CallQuery struct {
	UserID int64
	Status int
	Page   int
	Size   int
}

// OpLogQuery 操作日志筛选。
type OpLogQuery struct {
	AdminID int64
	Module  string
	Page    int
	Size    int
}

// 状态文案与常量保持与 account 模块一致，避免后台显示"2"而登录报错说"已冻结"。
const (
	StatusNormal  = 1
	StatusFrozen  = 2
	StatusBanned  = 3
	StatusDeleted = 4
)

func statusText(status int) string {
	switch status {
	case StatusNormal:
		return "正常"
	case StatusFrozen:
		return "已冻结"
	case StatusBanned:
		return "已封禁"
	case StatusDeleted:
		return "已注销"
	default:
		return "未知"
	}
}

// ValidStatus 允许后台设置的状态。
// 不开放"注销"（4）：注销涉及数据删除与留存策略，不能由运营一键完成。
func ValidStatus(status int) bool {
	return status == StatusNormal || status == StatusFrozen || status == StatusBanned
}

var orderStatusText = map[int]string{
	0: "待支付",
	1: "已支付",
	2: "已关闭",
	3: "已退款",
}

var callStatusText = map[int]string{
	0: "初始",
	1: "振铃中",
	2: "通话中",
	3: "已结束",
	4: "已取消",
	5: "已拒接",
	6: "超时未接",
	7: "异常",
}

var endReasonText = map[int]string{
	0: "-",
	1: "正常挂断",
	2: "余额不足",
	3: "心跳超时",
	4: "被叫拒接",
	5: "网络异常",
	6: "风控中断",
	7: "达到上限",
	8: "超时未接",
}

func textOf(m map[int]string, v int) string {
	if s, ok := m[v]; ok {
		return s
	}
	return "未知"
}
