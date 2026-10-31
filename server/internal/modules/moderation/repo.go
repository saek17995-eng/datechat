package moderation

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Repository 数据访问。
type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

// ---- 举报 ----

// InsertReport 落一条举报。重复举报由唯一键 uk_reporter_target 拦截。
func (r *Repository) InsertReport(ctx context.Context, rep *Report) error {
	return r.db.WithContext(ctx).Table("report").Create(rep).Error
}

// ErrReportDuplicate 重复举报。
var ErrReportDuplicate = errors.New("report: 重复举报")

// IsDuplicate 判断是否为唯一键冲突。
// 除了 gorm 的哨兵错误，还兜住错误串里的 1062：
// 不同驱动/版本对 MySQL 重复键的包装不一致，只认哨兵会漏判成 500，
// 让"重复举报"在监控里伪装成服务故障。
func IsDuplicate(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "1062") || strings.Contains(msg, "Duplicate entry")
}

// CountReportToday 统计某用户当日举报数（限频用）。
func (r *Repository) CountReportToday(ctx context.Context, uid int64, day time.Time) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Table("report").
		Where("reporter_id = ? AND created_at >= ?", uid, day).
		Count(&n).Error
	return n, err
}

// FindReport 按 ID 查举报。
func (r *Repository) FindReport(ctx context.Context, id int64) (*Report, error) {
	var out Report
	err := r.db.WithContext(ctx).Table("report").Where("id = ?", id).Take(&out).Error
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ListReports 后台举报列表，按状态过滤（0 表示全部）。
func (r *Repository) ListReports(ctx context.Context, status int, page, size int) ([]Report, int64, error) {
	q := r.db.WithContext(ctx).Table("report")
	if status > 0 {
		q = q.Where("status = ?", status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []Report
	err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error
	return list, total, err
}

// UpdateReportHandle 写入处置结论。
func (r *Repository) UpdateReportHandle(ctx context.Context, id, handlerID int64, status int, result, note string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Table("report").Where("id = ?", id).
		Updates(map[string]any{
			"status":        status,
			"handler_id":    handlerID,
			"handle_result": result,
			"handle_note":   note,
			"handled_at":    now,
		}).Error
}

// ---- 审核留痕 ----

// InsertModeration 落一条审核记录。
func (r *Repository) InsertModeration(ctx context.Context, rec *ModerationRecord) error {
	return r.db.WithContext(ctx).Table("moderation_record").Create(rec).Error
}

// ListModeration 后台审核列表，result=0 表示全部，3 常用于"待人工"。
func (r *Repository) ListModeration(ctx context.Context, result int, page, size int) ([]ModerationRecord, int64, error) {
	q := r.db.WithContext(ctx).Table("moderation_record")
	if result > 0 {
		q = q.Where("result = ?", result)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []ModerationRecord
	err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error
	return list, total, err
}

// UpdateModerationHandle 人工复核结论。
func (r *Repository) UpdateModerationHandle(ctx context.Context, id, handlerID int64, result int, note string) error {
	return r.db.WithContext(ctx).Table("moderation_record").Where("id = ?", id).
		Updates(map[string]any{
			"result":      result,
			"handled_by":  handlerID,
			"handled_at":  time.Now(),
			"handle_note": note,
		}).Error
}

// ---- 用户处置 ----

// UserStatus 读取用户当前状态。
func (r *Repository) UserStatus(ctx context.Context, uid int64) (int, error) {
	var status int
	err := r.db.WithContext(ctx).Table("user").
		Where("id = ? AND deleted_at IS NULL", uid).Pluck("status", &status).Error
	return status, err
}

// UpdateUserStatus 变更用户状态（冻结/封禁/解封）。ban 时同时清掉 IM 之外的可见性由上层决定。
func (r *Repository) UpdateUserStatus(ctx context.Context, uid int64, status int) error {
	return r.db.WithContext(ctx).Table("user").Where("id = ?", uid).
		Update("status", status).Error
}

// ---- 后台账号 ----

// FindAdminByUsername 按登录名查后台账号。
func (r *Repository) FindAdminByUsername(ctx context.Context, username string) (*AdminUser, error) {
	var out AdminUser
	err := r.db.WithContext(ctx).Table("admin_user").Where("username = ?", username).Take(&out).Error
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// InsertAdmin 新建后台账号。
func (r *Repository) InsertAdmin(ctx context.Context, a *AdminUser) error {
	return r.db.WithContext(ctx).Table("admin_user").Create(a).Error
}

// TouchAdminLogin 更新最后登录信息。
func (r *Repository) TouchAdminLogin(ctx context.Context, id int64, ip string) error {
	return r.db.WithContext(ctx).Table("admin_user").Where("id = ?", id).
		Updates(map[string]any{"last_login_at": time.Now(), "last_login_ip": ip}).Error
}

// CountAdmin 统计后台账号数，用于判断是否需要初始化超管。
func (r *Repository) CountAdmin(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Table("admin_user").Count(&n).Error
	return n, err
}

// ---- 操作审计 ----

// InsertOpLog 写后台操作审计。
func (r *Repository) InsertOpLog(ctx context.Context, adminID int64, adminName, module, action, targetType, targetID, before, after, note, ip string) error {
	row := map[string]any{
		"admin_id":    adminID,
		"admin_name":  adminName,
		"module":      module,
		"action":      action,
		"target_type": targetType,
		"target_id":   targetID,
		"before_json": nullableJSON(before),
		"after_json":  nullableJSON(after),
		"note":        note,
		"ip":          ip,
	}
	return r.db.WithContext(ctx).Table("admin_op_log").Create(row).Error
}

// nullableJSON 空串写 NULL，避免 JSON 列里塞一个空字符串。
func nullableJSON(s string) any {
	if s == "" {
		return nil
	}
	return s
}
