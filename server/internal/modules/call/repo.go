package call

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Repository 通话数据访问。
//
// 所有状态跃迁都必须走 UpdateState 的条件更新：先读后写在高并发下会出现
// "两端同时接通"这类双花问题，用 WHERE status IN (...) 让数据库来做裁决。
type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

func (r *Repository) conn(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx)
}

// InsertSession 创建会话并回填主键。
func (r *Repository) InsertSession(ctx context.Context, s *Session) error {
	if err := r.conn(ctx).Table("call_session").Create(s).Error; err != nil {
		return fmt.Errorf("call: 创建通话失败: %w", err)
	}
	return nil
}

// FindByID 按主键查会话。
func (r *Repository) FindByID(ctx context.Context, id int64) (*Session, error) {
	var s Session
	err := r.conn(ctx).Table("call_session").Where("id = ?", id).Take(&s).Error
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// FindByCallNo 按通话单号查会话。
func (r *Repository) FindByCallNo(ctx context.Context, callNo string) (*Session, error) {
	var s Session
	err := r.conn(ctx).Table("call_session").Where("call_no = ?", callNo).Take(&s).Error
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// UpdateState 条件更新状态。from 为允许的原状态集合，to 为目标状态。
// 返回 RowsAffected>0 表示本次抢到了跃迁权；并发的第二次会得到 false（不是错误）。
func (r *Repository) UpdateState(ctx context.Context, id int64, from []int, to int, fields map[string]any) (bool, error) {
	if len(from) == 0 {
		return false, fmt.Errorf("call: 状态跃迁必须声明原状态")
	}
	updates := map[string]any{"status": to}
	for k, v := range fields {
		updates[k] = v
	}
	res := r.conn(ctx).Table("call_session").
		Where("id = ? AND status IN ?", id, from).
		Updates(updates)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ListSessions 通话记录：主叫与被叫视角合并，按时间倒序。
func (r *Repository) ListSessions(ctx context.Context, uid int64, page, size int) ([]Session, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	q := r.conn(ctx).Table("call_session").Where("caller_id = ? OR callee_id = ?", uid, uid)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []Session
	if err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Scan(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ActiveSession 查询用户进行中的会话（振铃或通话中），用于防并发呼叫。
func (r *Repository) ActiveSession(ctx context.Context, uid int64) (*Session, error) {
	var s Session
	err := r.conn(ctx).Table("call_session").
		Where("(caller_id = ? OR callee_id = ?) AND status IN ?", uid, uid, []int{StatusRinging, StatusTalking}).
		Order("id DESC").Take(&s).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

// InsertEvent 写状态机事件流水。
func (r *Repository) InsertEvent(ctx context.Context, sessionID int64, event string, from, to int, operator int64, payload string) error {
	return r.conn(ctx).Table("call_event_log").Create(map[string]any{
		"call_session_id": sessionID,
		"event":           event,
		"from_status":     from,
		"to_status":       to,
		"operator_id":     operator,
		"payload":         nullableJSON(payload),
	}).Error
}

// ListEvents 读取事件流水，客诉排查与后台详情都用得上。
func (r *Repository) ListEvents(ctx context.Context, sessionID int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var list []Event
	if err := r.conn(ctx).Table("call_event_log").
		Where("call_session_id = ?", sessionID).Order("id ASC").Limit(limit).Scan(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// FreezeSegment 预扣第 seq 分钟，天然幂等（uk_session_seq）。
// 返回 true 表示本次真正插入；false 表示该分钟已扣过，调用方应视为成功而不是错误。
func (r *Repository) FreezeSegment(ctx context.Context, seg *Segment) (bool, error) {
	err := r.conn(ctx).Table("call_segment").Create(seg).Error
	if err != nil {
		if isDuplicate(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ListSegments 读取分钟切片，对账与详情展示用。
func (r *Repository) ListSegments(ctx context.Context, sessionID int64) ([]Segment, error) {
	var list []Segment
	if err := r.conn(ctx).Table("call_segment").
		Where("call_session_id = ?", sessionID).Order("seq_no ASC").Scan(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// SettleSegments 把未结算切片标记为已结算，并回填实际秒数。
func (r *Repository) SettleSegments(ctx context.Context, sessionID int64, startedAt, endedAt time.Time, perMin int64) error {
	segs, err := r.ListSegments(ctx, sessionID)
	if err != nil {
		return err
	}
	for _, s := range segs {
		if s.Status == SegmentSettled {
			continue
		}
		start := startedAt.Add(time.Duration(s.SeqNo-1) * time.Minute)
		end := start.Add(time.Minute)
		if endedAt.Before(end) {
			end = endedAt
		}
		seconds := int(end.Sub(start).Seconds())
		if seconds < 0 {
			seconds = 0
		}
		if err := r.conn(ctx).Table("call_segment").Where("id = ?", s.ID).
			Updates(map[string]any{
				"end_at":  end,
				"seconds": seconds,
				"coins":   perMin,
				"status":  SegmentSettled,
			}).Error; err != nil {
			return err
		}
	}
	return nil
}

// ListRingTimeout 超时未接的会话。
func (r *Repository) ListRingTimeout(ctx context.Context, before time.Time, limit int) ([]Session, error) {
	var list []Session
	if err := r.conn(ctx).Table("call_session").
		Where("status = ? AND created_at < ?", StatusRinging, before).
		Order("id ASC").Limit(limit).Scan(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListBeatTimeout 心跳超时的通话中会话。
func (r *Repository) ListBeatTimeout(ctx context.Context, before time.Time, limit int) ([]Session, error) {
	var list []Session
	if err := r.conn(ctx).Table("call_session").
		Where("status = ? AND last_beat_at IS NOT NULL AND last_beat_at < ?", StatusTalking, before).
		Order("id ASC").Limit(limit).Scan(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListTalking 所有通话中会话（后台巡检续扣用）。
func (r *Repository) ListTalking(ctx context.Context, limit int) ([]Session, error) {
	var list []Session
	if err := r.conn(ctx).Table("call_session").
		Where("status = ?", StatusTalking).Order("id ASC").Limit(limit).Scan(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// FindPrice 读取生效定价：被叫自定价优先，其次全局价。
// 定价在下单时快照进 call_session，之后调价不影响进行中的通话——
// 这是"下单快照"的意义，否则一次调价会让正在通话的用户账单突变。
func (r *Repository) FindPrice(ctx context.Context, calleeID int64, mediaType int) (*Price, error) {
	type row struct {
		AudioPerMin  int `gorm:"column:audio_per_min"`
		VideoPerMin  int `gorm:"column:video_per_min"`
		PlatformRate int `gorm:"column:platform_rate"`
	}
	var r1 row
	err := r.conn(ctx).Table("call_price_config").
		Select("audio_per_min", "video_per_min", "platform_rate").
		Where("scope = 2 AND user_id = ? AND status = 1 AND (effective_at IS NULL OR effective_at <= NOW())", calleeID).
		Order("id DESC").Limit(1).Scan(&r1).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	mode := PriceCustom
	if r1.AudioPerMin == 0 && r1.VideoPerMin == 0 {
		mode = PriceGlobal
		var r2 row
		e := r.conn(ctx).Table("call_price_config").
			Select("audio_per_min", "video_per_min", "platform_rate").
			Where("scope = 1 AND status = 1 AND (effective_at IS NULL OR effective_at <= NOW())").
			Order("id ASC").Limit(1).Scan(&r2).Error
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return nil, e
		}
		r1 = r2
	}
	perMin := r1.AudioPerMin
	if mediaType == MediaVideo {
		perMin = r1.VideoPerMin
	}
	rate := r1.PlatformRate
	if rate <= 0 {
		rate = 300 // 兜底 30%，避免"抽成 0"被当成"平台不抽成"而静默亏损
	}
	return &Price{PerMin: int64(perMin), PlatformRate: rate, Mode: mode}, nil
}

// BlockedEitherWay 双向拉黑判定：任一方拉黑对方都不允许通话。
func (r *Repository) BlockedEitherWay(ctx context.Context, a, b int64) (bool, error) {
	var n int64
	err := r.conn(ctx).Raw(
		`SELECT COUNT(1) FROM user_block
		 WHERE deleted_at IS NULL
		   AND ((user_id = ? AND blocked_user_id = ?) OR (user_id = ? AND blocked_user_id = ?))`,
		a, b, b, a,
	).Scan(&n).Error
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// UserStatus 查询用户状态（1 正常）。
func (r *Repository) UserStatus(ctx context.Context, uid int64) (int, error) {
	var status sql.NullInt64
	if err := r.conn(ctx).Raw("SELECT status FROM `user` WHERE id = ? AND deleted_at IS NULL", uid).Scan(&status).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return int(status.Int64), nil
}

// RealnameVerified 查询用户是否已实名（realname_status = 2 已认证）。
func (r *Repository) RealnameVerified(ctx context.Context, uid int64) (bool, error) {
	var n int64
	if err := r.conn(ctx).Raw(
		"SELECT COUNT(1) FROM user_profile WHERE user_id = ? AND realname_status = 2", uid,
	).Scan(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// SumSegmentCoins 统计某通话的切片扣费合计，用于对账校验（应与 call_session.total_coins 相等）。
func (r *Repository) SumSegmentCoins(ctx context.Context, sessionID int64) (int64, error) {
	var total sql.NullInt64
	if err := r.conn(ctx).Raw(
		"SELECT SUM(coins) FROM call_segment WHERE call_session_id = ? AND status = ?", sessionID, SegmentSettled,
	).Scan(&total).Error; err != nil {
		return 0, err
	}
	return total.Int64, nil
}

func nullableJSON(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func isDuplicate(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "1062") || strings.Contains(msg, "Duplicate entry")
}
