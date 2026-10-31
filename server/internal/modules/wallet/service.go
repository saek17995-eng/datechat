package wallet

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/datechat/server/internal/config"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/wxpay"
)

// Service 钱包与充值服务。
type Service struct {
	cfg  *config.Config
	log  *zap.Logger
	repo *Repository
	pay  PayClient
	now  func() time.Time
}

// NewService 构造服务。
func NewService(cfg *config.Config, log *zap.Logger, repo *Repository, pay PayClient) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{cfg: cfg, log: log, repo: repo, pay: pay, now: time.Now}
}

// Me 我的钱包。
func (s *Service) Me(ctx context.Context, uid int64) (*Wallet, error) {
	if err := s.repo.EnsureWallet(ctx, uid); err != nil {
		return nil, err
	}
	return s.repo.GetWallet(ctx, uid)
}

// Transactions 金币流水。
func (s *Service) Transactions(ctx context.Context, uid int64, page, size int) ([]Transaction, int64, error) {
	return s.repo.ListTransactions(ctx, uid, page, size)
}

// Balance 查询可用金币。通话模块在发起/续扣前用它做余额校验。
func (s *Service) Balance(ctx context.Context, uid int64) (int64, error) {
	w, err := s.repo.GetWallet(ctx, uid)
	if err != nil {
		return 0, err
	}
	return w.CoinBalance, nil
}

// Charge 通话扣费（主叫侧）。
//
// bizID 必须是"通话号#分钟序号"这类天然幂等键：心跳重发、客户端重试、
// 后台巡检并发触发，都只会真正扣一次。余额不足返回 errs.ErrBalanceNotEnough，
// 由通话模块决定挂断而不是让余额变负数。
func (s *Service) Charge(ctx context.Context, uid int64, bizID string, coins int64, remark string) (int64, error) {
	if uid <= 0 || bizID == "" {
		return 0, errs.ErrInvalidParam.WithMsg("扣费参数不完整")
	}
	_, balance, err := s.repo.ApplyFlow(ctx, uid, BizConsume, bizID, DirOut, coins, remark)
	if err != nil {
		return 0, err
	}
	return balance, nil
}

// Income 通话收益入账（被叫侧）。同样带幂等键，重复结算不会重复打款。
func (s *Service) Income(ctx context.Context, uid int64, bizID string, coins int64, remark string) error {
	if uid <= 0 || bizID == "" {
		return errs.ErrInvalidParam.WithMsg("结算参数不完整")
	}
	if coins <= 0 {
		return nil
	}
	_, _, err := s.repo.ApplyFlow(ctx, uid, BizCallIncome, bizID, DirIn, coins, remark)
	return err
}

// Packages 充值档位。
func (s *Service) Packages(ctx context.Context) ([]Package, error) {
	return s.repo.ListPackages(ctx)
}

// CreateOrder 创建充值订单并（在真实通道下）调用微信下单。
//
// 金额与金币完全取自档位表：客户端只传 package_id。
// 接受客户端传金额是这类系统最典型的被薅方式——1 分钱买 10000 金币。
func (s *Service) CreateOrder(ctx context.Context, uid int64, packageID int64, clientIP string) (*Order, map[string]any, error) {
	pkg, err := s.repo.FindPackage(ctx, packageID)
	if err != nil {
		return nil, nil, errs.ErrNotFound.WithMsg("充值档位不存在或已下架")
	}
	now := s.now()

	// P8 合规：未实名用户限制单日充值额度。
	// 额度不是禁止充值——完全不让充会把新用户挡在门外，
	// 这里只是把"匿名大额充值"这条风险路径收窄。
	if s.cfg != nil {
		pol := s.cfg.Compliance.Policy()
		if pol.MaxRechargeFenPerDay > 0 {
			verified, verr := s.repo.RealnameVerified(ctx, uid)
			if verr != nil {
				s.log.Warn("查询实名状态失败，按未实名处理", zap.Int64("uid", uid), zap.Error(verr))
			}
			if !verified {
				used, uerr := s.repo.SumRechargeFenSince(ctx, uid, startOfDay(now))
				if uerr != nil {
					s.log.Warn("统计今日充值失败", zap.Int64("uid", uid), zap.Error(uerr))
				}
				if used+int64(pkg.PriceFen) > int64(pol.MaxRechargeFenPerDay) {
					left := int64(pol.MaxRechargeFenPerDay) - used
					if left < 0 {
						left = 0
					}
					return nil, nil, errs.ErrForbidden.WithMsg(
						"未实名用户单日充值上限 %d 元，今日剩余 %.2f 元，完成实名认证后可解除限制",
						pol.MaxRechargeFenPerDay/100, float64(left)/100)
				}
			}
		}
	}

	orderNo := NewOrderNo(now, randomUint32())
	expireMin := 30
	if s.cfg != nil && s.cfg.Pay.OrderExpireMin > 0 {
		expireMin = s.cfg.Pay.OrderExpireMin
	}
	expireAt := now.Add(time.Duration(expireMin) * time.Minute)

	o := &Order{
		OrderNo:   orderNo,
		UserID:    uid,
		PackageID: pkg.ID,
		AmountFen: pkg.PriceFen,
		Coins:     int(pkg.TotalCoins()),
		Channel:   ChannelWechat,
		Status:    OrderPending,
		ClientIP:  clientIP,
		ExpireAt:  &expireAt,
	}
	if err := s.repo.InsertOrder(ctx, o); err != nil {
		return nil, nil, errs.ErrInternal.WithErr(err)
	}

	var params map[string]any
	if s.pay != nil && s.pay.Enabled() {
		prepay, err := s.pay.Prepay(ctx, wxpay.Order{
			OrderNo:   orderNo,
			AmountFen: pkg.PriceFen,
			Desc:      fmt.Sprintf("金币充值%d枚", pkg.TotalCoins()),
			ClientIP:  clientIP,
		})
		if err != nil {
			// 下单失败不删单：用户可能稍后付款，留待查单/关单处理。
			s.log.Error("微信下单失败", zap.String("order_no", orderNo), zap.Error(err))
			return nil, nil, errs.ErrInternal.WithMsg("支付下单失败，请稍后重试")
		}
		params = prepay.PayParams
	} else {
		// 本地/未配置支付：返回空参数，由 /mock-paid 或后台补单推进状态。
		params = map[string]any{"mock": true, "order_no": orderNo}
	}
	return o, params, nil
}

// HandleNotify 处理支付成功回调。
//
// 幂等三道闸门：
//  1. pay_notify_log 唯一键先挡住"同一交易号同一事件"的重复投递；
//  2. 订单状态条件更新保证只有待支付订单能变成已支付；
//  3. wallet_transaction 的 uk_biz 保证同一订单只写一条流水（真并发下整笔回滚）。
func (s *Service) HandleNotify(ctx context.Context, n *wxpay.Notify, raw string) error {
	if n == nil || n.OrderNo == "" {
		return errs.ErrInvalidParam.WithMsg("回调缺少订单号")
	}
	order, err := s.repo.FindOrder(ctx, n.OrderNo)
	if err != nil {
		s.log.Warn("回调订单不存在", zap.String("order_no", n.OrderNo))
		_, _ = s.repo.InsertNotifyLog(ctx, ChannelWechat, n.OrderNo, n.TradeNo, n.EventType, raw, NotifyFailed, "订单不存在")
		return errs.ErrNotFound.WithMsg("订单不存在")
	}

	rawJSON, _ := json.Marshal(map[string]any{
		"order_no": n.OrderNo, "trade_no": n.TradeNo, "amount_fen": n.AmountFen, "event": n.EventType,
	})
	first, err := s.repo.InsertNotifyLog(ctx, ChannelWechat, n.OrderNo, n.TradeNo, n.EventType, string(rawJSON), NotifyPending, "")
	if err != nil {
		s.log.Error("写回调日志失败", zap.Error(err))
		return errs.ErrInternal.WithErr(err)
	}
	if !first {
		s.log.Info("重复回调已忽略", zap.String("order_no", n.OrderNo), zap.String("trade_no", n.TradeNo))
		_ = s.repo.BumpNotifyCount(ctx, n.OrderNo)
		return nil
	}

	// 金额校验：回调金额必须与订单一致，防止"改包降价"。
	if n.AmountFen > 0 && n.AmountFen != order.AmountFen {
		s.log.Error("回调金额与订单不符",
			zap.String("order_no", n.OrderNo), zap.Int("order", order.AmountFen), zap.Int("notify", n.AmountFen))
		_, _ = s.repo.InsertNotifyLog(ctx, ChannelWechat, n.OrderNo, n.TradeNo, n.EventType, string(rawJSON), NotifyFailed, "金额不符")
		return errs.ErrInvalidParam.WithMsg("回调金额与订单不一致")
	}

	paidAt := n.PaidAt
	if paidAt.IsZero() {
		paidAt = s.now()
	}
	applied, err := s.repo.MarkPaid(ctx, order, int64(order.Coins), n.TradeNo, paidAt)
	if err != nil {
		s.log.Error("入账失败", zap.String("order_no", n.OrderNo), zap.Error(err))
		return errs.ErrInternal.WithErr(err)
	}
	if !applied {
		s.log.Info("订单已支付，本次未重复加币", zap.String("order_no", n.OrderNo))
		return nil
	}
	s.log.Info("充值到账", zap.String("order_no", n.OrderNo), zap.Int64("uid", order.UserID), zap.Int("coins", order.Coins))
	return nil
}

// SyncOrder 主动查单补单：回调丢失时的兜底（用户付款成功但没收到回调）。
func (s *Service) SyncOrder(ctx context.Context, orderNo string) (*Order, bool, error) {
	order, err := s.repo.FindOrder(ctx, orderNo)
	if err != nil {
		return nil, false, errs.ErrNotFound.WithMsg("订单不存在")
	}
	if order.Status == OrderPaid {
		_ = s.repo.MarkReconciled(ctx, orderNo)
		return order, false, nil
	}
	if s.pay == nil || !s.pay.Enabled() {
		return order, false, errs.ErrServiceBusy.WithMsg("支付通道未配置，无法查单")
	}
	n, err := s.pay.Query(ctx, orderNo)
	if err != nil {
		return nil, false, errs.ErrInternal.WithErr(err)
	}
	if n == nil || n.TradeNo == "" {
		return order, false, nil
	}
	if err := s.HandleNotify(ctx, n, ""); err != nil {
		return nil, false, err
	}
	_ = s.repo.MarkReconciled(ctx, orderNo)
	fresh, _ := s.repo.FindOrder(ctx, orderNo)
	return fresh, true, nil
}

// MockPaid 本地模拟支付成功。仅非生产且配置开启时可用，用于端到端验收。
func (s *Service) MockPaid(ctx context.Context, orderNo string) error {
	if s.cfg != nil && s.cfg.IsProd() {
		return errs.ErrForbidden.WithMsg("生产环境禁止模拟支付")
	}
	if s.cfg != nil && !s.cfg.Pay.MockEnabled {
		return errs.ErrForbidden.WithMsg("未开启模拟支付（pay.mock_enabled）")
	}
	order, err := s.repo.FindOrder(ctx, orderNo)
	if err != nil {
		return errs.ErrNotFound.WithMsg("订单不存在")
	}
	if order.Status == OrderPaid {
		return nil
	}
	tradeNo := "MOCK" + orderNo
	return s.HandleNotify(ctx, &wxpay.Notify{
		OrderNo:   orderNo,
		TradeNo:   tradeNo,
		EventType: "MOCK.SUCCESS",
		AmountFen: order.AmountFen,
		PaidAt:    s.now(),
	}, "")
}

// Reconcile 对账：返回待处理订单数与余额/流水的一致性结论。
func (s *Service) Reconcile(ctx context.Context, sinceHours int) (*ReconcileReport, error) {
	if sinceHours <= 0 {
		sinceHours = 24
	}
	since := s.now().Add(-time.Duration(sinceHours) * time.Hour)
	orders, err := s.repo.ListUnreconciled(ctx, since, 500)
	if err != nil {
		return nil, err
	}
	balance, err := s.repo.SumBalance(ctx)
	if err != nil {
		return nil, err
	}
	sumIn, err := s.repo.SumRechargeCoins(ctx)
	if err != nil {
		return nil, err
	}
	report := &ReconcileReport{
		PendingOrders: len(orders),
		WalletBalance: balance,
		RechargeCoins: sumIn,
		Balanced:      balance == sumIn,
	}
	// 逐单查单补正：只处理已支付但未对账的，以及超时未付的关单。
	for _, o := range orders {
		if o.Status == OrderPaid {
			_ = s.repo.MarkReconciled(ctx, o.OrderNo)
			report.Reconciled++
			continue
		}
		if o.ExpireAt != nil && o.ExpireAt.Before(s.now()) {
			if _, err := s.repo.CloseExpired(ctx, s.now(), 1); err == nil {
				report.Closed++
			}
		}
	}
	return report, nil
}

// ReconcileReport 对账结论。
type ReconcileReport struct {
	PendingOrders int   `json:"pending_orders"`
	Reconciled    int   `json:"reconciled"`
	Closed        int   `json:"closed"`
	WalletBalance int64 `json:"wallet_balance"`
	RechargeCoins int64 `json:"recharge_coins"`
	// Balanced 为 false 说明"钱包余额"与"充值流水累计"对不上，
	// 只可能是消费流水异常或有人直接改了余额表，必须人工介入。
	Balanced bool `json:"balanced"`
}

func randomUint32() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint32(time.Now().UnixNano())
	}
	return binary.BigEndian.Uint32(b[:])
}

var _ = errors.New

// startOfDay 取当天零点。单日额度的口径必须按自然日，
// 用"最近 24 小时"会出现跨天时额度被算两遍的偏差。
func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
