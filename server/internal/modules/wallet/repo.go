package wallet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/datechat/server/internal/pkg/errs"
)

// Repository 钱包数据访问。
// 所有余额变动都必须在事务里完成，且以 wallet_transaction 为唯一账本。
type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

// GetWallet 读取钱包，不存在时返回零值（上层决定是否初始化）。
func (r *Repository) GetWallet(ctx context.Context, uid int64) (*Wallet, error) {
	var w Wallet
	err := r.db.WithContext(ctx).Table("wallet").Where("user_id = ?", uid).Take(&w).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &Wallet{UserID: uid}, nil
		}
		return nil, err
	}
	return &w, nil
}

// EnsureWallet 保证钱包存在。注册时与首次充值前都会调用。
func (r *Repository) EnsureWallet(ctx context.Context, uid int64) error {
	return r.db.WithContext(ctx).Exec(
		"INSERT IGNORE INTO wallet (user_id) VALUES (?)", uid,
	).Error
}

// ListTransactions 金币流水，按时间倒序。
func (r *Repository) ListTransactions(ctx context.Context, uid int64, page, size int) ([]Transaction, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	q := r.db.WithContext(ctx).Table("wallet_transaction").Where("user_id = ?", uid)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []Transaction
	err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Scan(&list).Error
	return list, total, err
}

// ListPackages 上架的充值档位。
func (r *Repository) ListPackages(ctx context.Context) ([]Package, error) {
	var list []Package
	err := r.db.WithContext(ctx).Table("recharge_package").
		Where("status = 1").Order("sort DESC, id ASC").Scan(&list).Error
	return list, err
}

// FindPackage 按 ID 查档位（必须对 status 与渠道做校验，防止下单已下架档位）。
func (r *Repository) FindPackage(ctx context.Context, id int64) (*Package, error) {
	var p Package
	err := r.db.WithContext(ctx).Table("recharge_package").
		Where("id = ? AND status = 1", id).Take(&p).Error
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// FindOrder 按订单号查订单。
func (r *Repository) FindOrder(ctx context.Context, orderNo string) (*Order, error) {
	var o Order
	err := r.db.WithContext(ctx).Table("recharge_order").Where("order_no = ?", orderNo).Take(&o).Error
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// FindOrderByTradeNo 按第三方交易号查订单（幂等去重）。
func (r *Repository) FindOrderByTradeNo(ctx context.Context, channel int, tradeNo string) (*Order, error) {
	var o Order
	err := r.db.WithContext(ctx).Table("recharge_order").
		Where("channel = ? AND channel_trade_no = ?", channel, tradeNo).Take(&o).Error
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// InsertOrder 创建订单。
func (r *Repository) InsertOrder(ctx context.Context, o *Order) error {
	return r.db.WithContext(ctx).Table("recharge_order").Create(o).Error
}

// MarkPaid 在事务里完成"订单置为已支付 + 金币入账 + 写流水"。
//
// 幂等的关键：uk_biz(biz_type,biz_id,user_id) 保证同一笔充值只能写一条流水；
// 即使回调重复投递，第二次会因唯一键冲突而整笔回滚，不会重复加币。
func (r *Repository) MarkPaid(ctx context.Context, o *Order, coins int64, tradeNo string, paidAt time.Time) (bool, error) {
	applied := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1) 订单置为已支付；用 status 条件保证并发下只生效一次
		res := tx.Table("recharge_order").
			Where("order_no = ? AND status IN (?)", o.OrderNo, []int{OrderPending, OrderPaying}).
			Updates(map[string]any{
				"status":           OrderPaid,
				"channel_trade_no": tradeNo,
				"paid_at":          paidAt,
				"notify_count":     gorm.Expr("notify_count + 1"),
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// 已被处理过（重复回调）或订单状态不合法
			_ = tx.Table("recharge_order").Where("order_no = ?", o.OrderNo).
				Update("notify_count", gorm.Expr("notify_count + 1")).Error
			return ErrOrderPaid
		}

		// 2) 余额入账（乐观锁 + 行锁双保险）
		var before int64
		if err := tx.Raw("SELECT coin_balance FROM wallet WHERE user_id = ? FOR UPDATE", o.UserID).Scan(&before).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) || err == sql.ErrNoRows {
				if err := tx.Exec("INSERT IGNORE INTO wallet (user_id, coin_balance) VALUES (?, 0)", o.UserID).Error; err != nil {
					return err
				}
				before = 0
			} else {
				return err
			}
		}
		after := before + coins
		if err := tx.Exec(
			`UPDATE wallet SET coin_balance = ?, total_recharge = total_recharge + ?, version = version + 1
			 WHERE user_id = ?`, after, coins, o.UserID,
		).Error; err != nil {
			return err
		}

		// 3) 写流水；唯一键冲突会让整笔事务回滚，杜绝重复入账
		if err := tx.Exec(
			`INSERT INTO wallet_transaction
			 (user_id, biz_type, biz_id, direction, coins, balance_before, balance_after, remark)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			o.UserID, BizRecharge, o.OrderNo, DirIn, coins, before, after, "充值到账",
		).Error; err != nil {
			return err
		}
		applied = true
		return nil
	})
	if errors.Is(err, ErrOrderPaid) {
		return false, nil
	}
	return applied, err
}

// ApplyFlow 在事务里完成「余额变动 + 写流水」，是通话计费的唯一出口。
//
// 幂等由 uk_biz(biz_type, biz_id, user_id) 保证：同一个业务单号（如 {通话号}#{分钟序号}）
// 重复调用只会写一条流水，第二次直接返回 applied=false，绝不会重复扣款。
//
// dir=DirOut 时余额不足返回 errs.ErrBalanceNotEnough，调用方据此决定"自动挂断"。
func (r *Repository) ApplyFlow(ctx context.Context, uid int64, bizType int, bizID string,
	dir int, coins int64, remark string) (applied bool, balance int64, err error) {
	if coins <= 0 {
		// 零额流水既不写库也不算失败：通话按分钟计费时，0 单价的档位不该产生噪声流水。
		return false, 0, nil
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var before int64
		row := tx.Raw("SELECT coin_balance FROM wallet WHERE user_id = ? FOR UPDATE", uid).Scan(&before)
		if row.Error != nil {
			if errors.Is(row.Error, gorm.ErrRecordNotFound) || errors.Is(row.Error, sql.ErrNoRows) {
				if err := tx.Exec("INSERT IGNORE INTO wallet (user_id, coin_balance) VALUES (?, 0)", uid).Error; err != nil {
					return err
				}
				before = 0
			} else {
				return row.Error
			}
		} else if row.RowsAffected == 0 {
			if err := tx.Exec("INSERT IGNORE INTO wallet (user_id, coin_balance) VALUES (?, 0)", uid).Error; err != nil {
				return err
			}
			before = 0
		}

		var after int64
		if dir == DirIn {
			after = before + coins
		} else {
			after = before - coins
			if after < 0 {
				return errs.ErrBalanceNotEnough
			}
		}
		// total_recharge / total_consume 是统计列，只计入对应语义的流水，
		// 通话收益（BizCallIncome）不进 total_recharge，否则"累计充值"会被收益污染。
		extra := ""
		if dir == DirOut {
			extra = ", total_consume = total_consume + ?"
		} else if bizType == BizRecharge {
			extra = ", total_recharge = total_recharge + ?"
		}
		sql := `UPDATE wallet SET coin_balance = ?` + extra + `, version = version + 1 WHERE user_id = ?`
		args := []any{after}
		if extra != "" {
			args = append(args, coins)
		}
		args = append(args, uid)
		if err := tx.Exec(sql, args...).Error; err != nil {
			return err
		}

		res := tx.Exec(
			`INSERT INTO wallet_transaction
			 (user_id, biz_type, biz_id, direction, coins, balance_before, balance_after, remark)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			uid, bizType, bizID, dir, coins, before, after, remark,
		)
		if res.Error != nil {
			if isDuplicate(res.Error) {
				// 已处理过：读取当前余额后静默成功，保证"重复续扣"不会二次扣款。
				_ = tx.Raw("SELECT coin_balance FROM wallet WHERE user_id = ?", uid).Scan(&before).Error
				balance = before
				applied = false
				return nil
			}
			return res.Error
		}
		applied = true
		balance = after
		return nil
	})
	if err != nil {
		return false, 0, err
	}
	return applied, balance, nil
}

// BumpNotifyCount 未入账但被重复回调时累加计数，便于监控发现异常重试。
func (r *Repository) BumpNotifyCount(ctx context.Context, orderNo string) error {
	return r.db.WithContext(ctx).Table("recharge_order").Where("order_no = ?", orderNo).
		Update("notify_count", gorm.Expr("notify_count + 1")).Error
}

// InsertNotifyLog 记录回调。
// 唯一键 uk_trade_event 让"同一交易号的同一事件"只会落一条，
// 这是回调幂等的第一道闸门（在业务处理之前）。
func (r *Repository) InsertNotifyLog(ctx context.Context, channel int, orderNo, tradeNo, eventType, raw string, result int, errMsg string) (bool, error) {
	row := map[string]any{
		"channel":    channel,
		"order_no":   orderNo,
		"trade_no":   tradeNo,
		"event_type": eventType,
		"raw":        nullableJSON(raw),
		"result":     result,
		"error_msg":  errMsg,
	}
	err := r.db.WithContext(ctx).Table("pay_notify_log").Create(row).Error
	if err != nil {
		if isDuplicate(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ListUnreconciled 待对账订单（已支付但未标记对账，或长时间停留在待支付）。
func (r *Repository) ListUnreconciled(ctx context.Context, since time.Time, limit int) ([]Order, error) {
	var list []Order
	err := r.db.WithContext(ctx).Table("recharge_order").
		Where("(status = ? AND reconciled = 0) OR (status = ? AND created_at >= ?)", OrderPaid, OrderPending, since).
		Order("id ASC").Limit(limit).Scan(&list).Error
	return list, err
}

// MarkReconciled 标记已对账。
func (r *Repository) MarkReconciled(ctx context.Context, orderNo string) error {
	return r.db.WithContext(ctx).Table("recharge_order").Where("order_no = ?", orderNo).
		Updates(map[string]any{"reconciled": 1, "sync_count": gorm.Expr("sync_count + 1"), "last_sync_at": time.Now()}).Error
}

// CloseExpired 关闭超时未支付订单。
func (r *Repository) CloseExpired(ctx context.Context, now time.Time, limit int) (int64, error) {
	res := r.db.WithContext(ctx).Table("recharge_order").
		Where("status IN (?) AND expire_at IS NOT NULL AND expire_at < ?", []int{OrderPending, OrderPaying}, now).
		Limit(limit).Updates(map[string]any{"status": OrderClosed})
	return res.RowsAffected, res.Error
}

// SumBalance 统计当前金币总余额（对账用：与流水累计值应相等）。
func (r *Repository) SumBalance(ctx context.Context) (int64, error) {
	var total sql.NullInt64
	if err := r.db.WithContext(ctx).Raw("SELECT SUM(coin_balance) FROM wallet").Scan(&total).Error; err != nil {
		return 0, err
	}
	return total.Int64, nil
}

// SumRechargeCoins 统计充值流水的金币合计（对账用）。
func (r *Repository) SumRechargeCoins(ctx context.Context) (int64, error) {
	var total sql.NullInt64
	if err := r.db.WithContext(ctx).Raw(
		"SELECT SUM(coins) FROM wallet_transaction WHERE biz_type = ? AND direction = ?", BizRecharge, DirIn,
	).Scan(&total).Error; err != nil {
		return 0, err
	}
	return total.Int64, nil
}

// SumRechargeFenSince 统计某用户自 since 起已下单的充值金额（含未支付），用于合规额度限制。
// 含未支付：否则并发下多笔待支付订单可以轻松绕过额度。
func (r *Repository) SumRechargeFenSince(ctx context.Context, uid int64, since time.Time) (int64, error) {
	var total sql.NullInt64
	if err := r.db.WithContext(ctx).Raw(
		"SELECT SUM(amount_fen) FROM recharge_order WHERE user_id = ? AND created_at >= ? AND status IN (0,1)",
		uid, since,
	).Scan(&total).Error; err != nil {
		return 0, err
	}
	return total.Int64, nil
}

// RealnameVerified 是否已实名（realname_status = 2）。
func (r *Repository) RealnameVerified(ctx context.Context, uid int64) (bool, error) {
	var n int64
	if err := r.db.WithContext(ctx).Raw(
		"SELECT COUNT(1) FROM user_profile WHERE user_id = ? AND realname_status = 2", uid,
	).Scan(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

func nullableJSON(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func isDuplicate(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return contains(msg, "1062") || contains(msg, "Duplicate entry")
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

var _ = fmt.Sprintf
