package wallet

import (
	"context"
	"errors"
	"time"

	"github.com/datechat/server/internal/config"
	"github.com/datechat/server/internal/pkg/wxpay"
)

// 金币流水业务类型，与 wallet_transaction.biz_type 对齐。
const (
	BizRecharge   = 1 // 充值
	BizConsume    = 2 // 通话消费
	BizRefund     = 3 // 退款
	BizReward     = 4 // 活动奖励
	BizAdjust     = 5 // 人工调整
	BizCallIncome = 6 // 通话收益（被叫分成入账）
)

// 流水方向。
const (
	DirIn  = 1
	DirOut = 2
)

// 支付渠道。
const (
	ChannelWechat = 1
	ChannelAlipay = 2
	ChannelIAP    = 3
)

// 订单状态，与 recharge_order.status 对齐。
const (
	OrderPending  = 0 // 待支付
	OrderPaying   = 1 // 支付中
	OrderPaid     = 2 // 已支付
	OrderFailed   = 3 // 已失败
	OrderRefunded = 4 // 已退款
	OrderClosed   = 5 // 已关闭
)

// 回调处理结果，与 pay_notify_log.result 对齐。
const (
	NotifyPending = 0
	NotifySuccess = 1
	NotifyDup     = 2
	NotifyFailed  = 3
)

// ErrOrderPaid 订单已支付（重复回调时返回，调用方视为成功而不是错误）。
var ErrOrderPaid = errors.New("wallet: 订单已支付")

// Wallet 用户钱包。
type Wallet struct {
	UserID        int64
	CoinBalance   int64
	FrozenCoin    int64
	TotalRecharge int64
	TotalConsume  int64
	Version       int
}

// Transaction 金币流水。
type Transaction struct {
	ID            int64
	UserID        int64
	BizType       int
	BizID         string
	Direction     int
	Coins         int64
	BalanceBefore int64
	BalanceAfter  int64
	Remark        string
	CreatedAt     time.Time
}

// Package 充值档位。
type Package struct {
	ID         int64 `json:"id"`
	Coins      int   `json:"coins"`
	BonusCoins int   `json:"bonus_coins"`
	PriceFen   int   `json:"price_fen"`
	Channel    int   `json:"channel"`
	Sort       int   `json:"sort"`
	Status     int   `json:"status"`
}

// TotalCoins 到账金币（含赠送）。
func (p Package) TotalCoins() int64 { return int64(p.Coins) + int64(p.BonusCoins) }

// Order 充值订单。
type Order struct {
	ID             int64
	OrderNo        string
	UserID         int64
	PackageID      int64
	AmountFen      int
	Coins          int
	Channel        int
	ChannelTradeNo *string
	Status         int
	ClientIP       string
	NotifyCount    int
	SyncCount      int
	LastSyncAt     *time.Time
	Reconciled     int
	PaidAt         *time.Time
	ExpireAt       *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// PayClient 支付通道能力的最小接口，便于测试与本地模拟。
type PayClient interface {
	Enabled() bool
	Prepay(ctx context.Context, o wxpay.Order) (*wxpay.Prepay, error)
	Query(ctx context.Context, orderNo string) (*wxpay.Notify, error)
}

// Deps 构造依赖。
type Deps struct {
	Config *config.Config
	Pay    PayClient
	Now    func() time.Time
}

// NewOrderNo 生成商户订单号：R + yyyymmddHHMMSS + 6 位随机。
// 带时间前缀便于按订单号粗略排序，也避免多实例只用随机数撞号。
func NewOrderNo(now time.Time, rand uint32) string {
	return "R" + now.Format("20060102150405") + sprintf6(rand)
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
