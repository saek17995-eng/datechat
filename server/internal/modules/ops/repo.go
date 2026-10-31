package ops

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// Repository 运营后台数据访问。
//
// 全部走 Raw SQL：后台查询字段多、跨表多，用 ORM 拼出来可读性反而更差，
// 而且一旦生成了 SELECT *，把 phone_cipher 这类敏感列带出去就很难发现。
type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

// Overview 汇总今日与累计指标。
// 单条 SQL 里用子查询而不是跑十次 COUNT：后台概览每次点开都要查，
// 十次往返在高并发时是实打实的数据库压力。
func (r *Repository) Overview(ctx context.Context, since time.Time) (*Overview, error) {
	var out Overview
	err := r.db.WithContext(ctx).Raw(`
SELECT
  (SELECT COUNT(1) FROM user WHERE deleted_at IS NULL)                              AS users_total,
  (SELECT COUNT(1) FROM user WHERE deleted_at IS NULL AND created_at >= ?)           AS users_new_today,
  (SELECT COUNT(1) FROM user WHERE status = 3)                                       AS users_banned,
  (SELECT COUNT(1) FROM recharge_order WHERE status = 1 AND paid_at >= ?)            AS orders_paid_today,
  (SELECT COALESCE(SUM(amount_fen),0) FROM recharge_order WHERE status=1 AND paid_at >= ?) AS recharge_fen_today,
  (SELECT COALESCE(SUM(coins),0) FROM recharge_order WHERE status=1 AND paid_at >= ?)      AS recharge_coins_today,
  (SELECT COUNT(1) FROM call_session WHERE created_at >= ?)                          AS calls_today,
  (SELECT COALESCE(SUM(billable_minutes),0) FROM call_session WHERE created_at >= ?) AS call_minutes,
  (SELECT COALESCE(SUM(total_coins),0) FROM call_session WHERE created_at >= ?)      AS call_coins_today,
  (SELECT COUNT(1) FROM report WHERE status = 0)                                     AS reports_pending,
  (SELECT COUNT(1) FROM moderation_record WHERE result = 2)                          AS moderation_pending,
  (SELECT COUNT(1) FROM user_profile WHERE realname_status = 1)                      AS realname_pending
`, since, since, since, since, since, since, since).
		Scan(&out).Error
	if err != nil {
		return nil, err
	}
	out.GeneratedAt = time.Now()
	return &out, nil
}

// ListUsers 用户列表。
func (r *Repository) ListUsers(ctx context.Context, q UserQuery, phoneHMAC string) ([]UserRow, int64, error) {
	where := "WHERE u.deleted_at IS NULL"
	args := []any{}
	if q.Status > 0 {
		where += " AND u.status = ?"
		args = append(args, q.Status)
	}
	switch {
	case phoneHMAC != "":
		where += " AND u.phone_hmac = ?"
		args = append(args, phoneHMAC)
	case q.Keyword != "":
		where += " AND (u.nickname LIKE ? OR u.id = ?)"
		like := "%" + q.Keyword + "%"
		args = append(args, like, q.Keyword)
	}

	var total int64
	if err := r.db.WithContext(ctx).Raw("SELECT COUNT(1) FROM user u "+where, args...).
		Scan(&total).Error; err != nil {
		return nil, 0, err
	}

	limit, offset := pageSize(q.Page, q.Size)
	var rows []UserRow
	err := r.db.WithContext(ctx).Raw(`
SELECT u.id, u.nickname, u.gender, u.birthday, u.status, u.register_ip, u.last_active_at,
       u.created_at, u.ban_until, u.ban_reason, u.phone_cipher,
       c.city AS city_name,
       COALESCE(p.realname_status,0) AS realname_status,
       COALESCE(w.coin_balance,0)    AS coin_balance,
       COALESCE(w.total_recharge,0)  AS total_recharge,
       COALESCE(w.total_consume,0)   AS total_consume,
       (SELECT COUNT(1) FROM report rp WHERE rp.target_uid = u.id) AS report_count
FROM user u
LEFT JOIN region_city   c ON c.city_code = u.city_code
LEFT JOIN user_profile  p ON p.user_id = u.id
LEFT JOIN wallet        w ON w.user_id = u.id
`+where+` ORDER BY u.id DESC LIMIT ? OFFSET ?`,
		append(args, limit, offset)...).Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// GetUser 单个用户详情所需的基础行。查不到返回 (nil, nil)。
func (r *Repository) GetUser(ctx context.Context, uid int64) (*UserRow, error) {
	var row UserRow
	err := r.db.WithContext(ctx).Raw(`
SELECT u.id, u.nickname, u.gender, u.birthday, u.status, u.register_ip, u.last_active_at,
       u.created_at, u.ban_until, u.ban_reason, u.phone_cipher,
       c.city AS city_name,
       COALESCE(p.realname_status,0) AS realname_status,
       COALESCE(w.coin_balance,0)    AS coin_balance,
       COALESCE(w.total_recharge,0)  AS total_recharge,
       COALESCE(w.total_consume,0)   AS total_consume,
       (SELECT COUNT(1) FROM report rp WHERE rp.target_uid = u.id) AS report_count
FROM user u
LEFT JOIN region_city   c ON c.city_code = u.city_code
LEFT JOIN user_profile  p ON p.user_id = u.id
LEFT JOIN wallet        w ON w.user_id = u.id
WHERE u.id = ? AND u.deleted_at IS NULL`, uid).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.UID == 0 {
		return nil, nil
	}
	return &row, nil
}

// UserStats 用户的行为统计。
func (r *Repository) UserStats(ctx context.Context, uid int64) (blocked, blockedBy, calls int64, seconds int64, income int64, err error) {
	err = r.db.WithContext(ctx).Raw(`
SELECT
  (SELECT COUNT(1) FROM user_block WHERE deleted_at IS NULL AND user_id = ?)              AS blocked,
  (SELECT COUNT(1) FROM user_block WHERE deleted_at IS NULL AND blocked_user_id = ?)      AS blocked_by,
  (SELECT COUNT(1) FROM call_session WHERE caller_id = ? OR callee_id = ?)                AS calls,
  (SELECT COALESCE(SUM(duration_sec),0) FROM call_session WHERE caller_id = ? OR callee_id = ?) AS seconds,
  (SELECT COALESCE(SUM(payee_income),0) FROM call_session WHERE callee_id = ?)            AS income
`, uid, uid, uid, uid, uid, uid, uid).Row().Scan(&blocked, &blockedBy, &calls, &seconds, &income)
	return
}

// RecentReports 最近被举报记录。
func (r *Repository) RecentReports(ctx context.Context, uid int64, limit int) ([]ReportRow, error) {
	var rows []ReportRow
	err := r.db.WithContext(ctx).Raw(`
SELECT id, reason, status, COALESCE(handle_result,'') AS handle_result, created_at
FROM report WHERE target_uid = ? ORDER BY id DESC LIMIT ?`, uid, limit).Scan(&rows).Error
	return rows, err
}

// SetUserStatus 改状态；banUntil 为空表示永久封禁。
func (r *Repository) SetUserStatus(ctx context.Context, uid int64, status int, banUntil *time.Time, reason string) error {
	return r.db.WithContext(ctx).Exec(
		`UPDATE user SET status = ?, ban_until = ?, ban_reason = ?, updated_at = NOW() WHERE id = ?`,
		status, banUntil, reason, uid,
	).Error
}

// ListOrders 充值订单。
func (r *Repository) ListOrders(ctx context.Context, q OrderQuery) ([]OrderRow, int64, error) {
	where := "WHERE 1=1"
	args := []any{}
	if q.UserID > 0 {
		where += " AND o.user_id = ?"
		args = append(args, q.UserID)
	}
	if q.Status >= 0 {
		where += " AND o.status = ?"
		args = append(args, q.Status)
	}
	if q.OrderNo != "" {
		where += " AND o.order_no = ?"
		args = append(args, q.OrderNo)
	}
	var total int64
	if err := r.db.WithContext(ctx).Raw("SELECT COUNT(1) FROM recharge_order o "+where, args...).
		Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	limit, offset := pageSize(q.Page, q.Size)
	var rows []OrderRow
	err := r.db.WithContext(ctx).Raw(`
SELECT o.id, o.order_no, o.user_id, COALESCE(u.nickname,'') AS nickname,
       o.amount_fen, o.coins, o.channel, o.status, o.paid_at, o.created_at
FROM recharge_order o LEFT JOIN user u ON u.id = o.user_id
`+where+` ORDER BY o.id DESC LIMIT ? OFFSET ?`,
		append(args, limit, offset)...).Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	for i := range rows {
		rows[i].StatusText = textOf(orderStatusText, rows[i].Status)
	}
	return rows, total, nil
}

// ListCalls 通话记录。
func (r *Repository) ListCalls(ctx context.Context, q CallQuery) ([]CallRow, int64, error) {
	where := "WHERE 1=1"
	args := []any{}
	if q.UserID > 0 {
		where += " AND (s.caller_id = ? OR s.callee_id = ?)"
		args = append(args, q.UserID, q.UserID)
	}
	if q.Status >= 0 {
		where += " AND s.status = ?"
		args = append(args, q.Status)
	}
	var total int64
	if err := r.db.WithContext(ctx).Raw("SELECT COUNT(1) FROM call_session s "+where, args...).
		Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	limit, offset := pageSize(q.Page, q.Size)
	var rows []CallRow
	err := r.db.WithContext(ctx).Raw(`
SELECT s.id, s.call_no, s.caller_id, s.callee_id, s.media_type, s.status,
       s.duration_sec, s.billable_minutes, s.total_coins, s.payee_income, s.platform_fee,
       s.end_reason, s.started_at, s.ended_at, s.created_at
FROM call_session s `+where+` ORDER BY s.id DESC LIMIT ? OFFSET ?`,
		append(args, limit, offset)...).Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	for i := range rows {
		rows[i].StatusText = textOf(callStatusText, rows[i].Status)
		rows[i].EndReasonText = textOf(endReasonText, rows[i].EndReason)
	}
	return rows, total, nil
}

// ListOpLogs 后台操作日志。
func (r *Repository) ListOpLogs(ctx context.Context, q OpLogQuery) ([]OpLogRow, int64, error) {
	where := "WHERE 1=1"
	args := []any{}
	if q.AdminID > 0 {
		where += " AND admin_id = ?"
		args = append(args, q.AdminID)
	}
	if q.Module != "" {
		where += " AND module = ?"
		args = append(args, q.Module)
	}
	var total int64
	if err := r.db.WithContext(ctx).Raw("SELECT COUNT(1) FROM admin_op_log "+where, args...).
		Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	limit, offset := pageSize(q.Page, q.Size)
	var rows []OpLogRow
	err := r.db.WithContext(ctx).Raw(`
SELECT id, admin_id, admin_name, module, action, target_type, target_id, COALESCE(note,'') AS note, created_at
FROM admin_op_log `+where+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, limit, offset)...).Scan(&rows).Error
	return rows, total, err
}

// InsertOpLog 写审计日志。后台每一次写操作都要留痕，失败必须让调用方知道。
func (r *Repository) InsertOpLog(ctx context.Context, adminID int64, adminName, module, action,
	targetType, targetID, before, after, note, ip string) error {
	return r.db.WithContext(ctx).Exec(
		`INSERT INTO admin_op_log
		   (admin_id, admin_name, module, action, target_type, target_id,
		    before_json, after_json, note, ip, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,NOW())`,
		adminID, adminName, module, action, targetType, targetID, before, after, note, ip,
	).Error
}

// pageSize 统一分页边界：后台列表不限制条数会有人一次性拉全表。
func pageSize(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	return size, (page - 1) * size
}

// fmtPhoneHMAC 只在调试日志里用，避免把真实手机号打进日志。
func fmtPhoneHMAC(hmac string) string {
	if len(hmac) > 8 {
		return hmac[:8] + "..."
	}
	return hmac
}

var _ = fmtPhoneHMAC
