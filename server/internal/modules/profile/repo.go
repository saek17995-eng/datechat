package profile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Repository 是资料模块的数据访问层。
type Repository struct {
	db *gorm.DB
}

// NewRepository 构造仓储。
func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

// ErrNoDB 表示仓储未注入数据库连接，属于装配错误。
var ErrNoDB = errors.New("profile: 数据库未初始化")

func (r *Repository) conn() (*gorm.DB, error) {
	if r == nil || r.db == nil {
		return nil, ErrNoDB
	}
	return r.db, nil
}

// FindUser 查用户，不存在返回 (nil, nil)。
func (r *Repository) FindUser(ctx context.Context, uid int64) (*User, error) {
	db, err := r.conn()
	if err != nil {
		return nil, err
	}
	var u User
	err = db.WithContext(ctx).
		Where("id = ? AND deleted_at IS NULL", uid).
		First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("profile: 查询用户失败: %w", err)
	}
	return &u, nil
}

// FindProfile 查扩展资料，不存在返回 (nil, nil)。
func (r *Repository) FindProfile(ctx context.Context, uid int64) (*UserProfile, error) {
	db, err := r.conn()
	if err != nil {
		return nil, err
	}
	var p UserProfile
	err = db.WithContext(ctx).Where("user_id = ?", uid).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("profile: 查询用户资料失败: %w", err)
	}
	return &p, nil
}

// SaveUserProfile 在一个事务里更新 user 与 user_profile。
//
// 必须同事务：昵称与签名分属两张表，只成功一半会让"我的资料"读回来是半新半旧，
// 用户会以为没保存成功而反复提交。
func (r *Repository) SaveUserProfile(ctx context.Context, u *User, p *UserProfile) error {
	db, err := r.conn()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if u != nil {
			cols := map[string]any{}
			if u.Nickname != "" {
				cols["nickname"] = u.Nickname
			}
			if u.Avatar != "" {
				cols["avatar"] = u.Avatar
			}
			if u.Gender != 0 {
				cols["gender"] = u.Gender
			}
			if u.Birthday != nil {
				cols["birthday"] = *u.Birthday
			}
			if u.CityCode != "" {
				cols["city_code"] = u.CityCode
			}
			if len(cols) > 0 {
				if err := tx.Model(&User{}).Where("id = ?", u.ID).Updates(cols).Error; err != nil {
					return fmt.Errorf("profile: 更新用户主表失败: %w", err)
				}
			}
		}
		if p != nil {
			if err := tx.Save(p).Error; err != nil {
				return fmt.Errorf("profile: 更新用户资料失败: %w", err)
			}
		}
		return nil
	})
}

// UpdateAvatar 只更新头像列。单独一个方法是因为头像提交有独立的校验与审计链路，
// 不希望它顺带把其他字段覆盖回去。
func (r *Repository) UpdateAvatar(ctx context.Context, uid int64, avatar string) error {
	db, err := r.conn()
	if err != nil {
		return err
	}
	if err := db.WithContext(ctx).
		Model(&User{}).
		Where("id = ?", uid).
		Update("avatar", avatar).Error; err != nil {
		return fmt.Errorf("profile: 更新头像失败: %w", err)
	}
	return nil
}

// UpdatePhotos 覆盖相册列表。
func (r *Repository) UpdatePhotos(ctx context.Context, uid int64, photos StringList) error {
	db, err := r.conn()
	if err != nil {
		return err
	}
	if err := db.WithContext(ctx).
		Model(&UserProfile{}).
		Where("user_id = ?", uid).
		Update("photos", photos).Error; err != nil {
		return fmt.Errorf("profile: 更新相册失败: %w", err)
	}
	return nil
}

// FindCity 按行政区划编码查城市，不存在返回 (nil, nil)。
func (r *Repository) FindCity(ctx context.Context, code string) (*City, error) {
	db, err := r.conn()
	if err != nil {
		return nil, err
	}
	var c City
	err = db.WithContext(ctx).Where("city_code = ? AND status = 1", code).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("profile: 查询城市失败: %w", err)
	}
	return &c, nil
}

// ListCities 返回启用的城市字典，按 sort 降序。
func (r *Repository) ListCities(ctx context.Context, limit int) ([]City, error) {
	db, err := r.conn()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var list []City
	if err := db.WithContext(ctx).
		Where("status = 1").
		Order("sort DESC, id ASC").
		Limit(limit).
		Find(&list).Error; err != nil {
		return nil, fmt.Errorf("profile: 查询城市列表失败: %w", err)
	}
	return list, nil
}

// InsertUploadFile 登记上传文件。
func (r *Repository) InsertUploadFile(ctx context.Context, f *UploadFile) error {
	db, err := r.conn()
	if err != nil {
		return err
	}
	if err := db.WithContext(ctx).Create(f).Error; err != nil {
		return fmt.Errorf("profile: 登记上传文件失败: %w", err)
	}
	return nil
}

// FindUploadByKey 按对象键查上传记录，不存在返回 (nil, nil)。
// 头像/相册提交时用它校验"这个地址确实是我们签发出去的"。
func (r *Repository) FindUploadByKey(ctx context.Context, key string) (*UploadFile, error) {
	db, err := r.conn()
	if err != nil {
		return nil, err
	}
	var f UploadFile
	err = db.WithContext(ctx).Where("object_key = ?", key).First(&f).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("profile: 查询上传记录失败: %w", err)
	}
	return &f, nil
}

// MarkUploadUsed 标记文件已被业务引用，并写入审核结论。
func (r *Repository) MarkUploadUsed(ctx context.Context, id int64, status int) error {
	db, err := r.conn()
	if err != nil {
		return err
	}
	now := time.Now()
	if err := db.WithContext(ctx).
		Model(&UploadFile{}).
		Where("id = ?", id).
		Updates(map[string]any{"used": 1, "status": status, "updated_at": now}).Error; err != nil {
		return fmt.Errorf("profile: 更新上传记录状态失败: %w", err)
	}
	return nil
}

// UpsertVisit 记录一次访问。同一对访客/被访者只保留一行，刷新访问时间。
func (r *Repository) UpsertVisit(ctx context.Context, visitorID, targetID int64, at time.Time) error {
	db, err := r.conn()
	if err != nil {
		return err
	}
	const q = `INSERT INTO user_visit (visitor_id, target_id, visited_at, created_at)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE visited_at = VALUES(visited_at)`
	if err := db.WithContext(ctx).Exec(q, visitorID, targetID, at, at).Error; err != nil {
		return fmt.Errorf("profile: 记录访问失败: %w", err)
	}
	return nil
}

// BlockRelation 查询两人之间的拉黑关系。
// blocked：viewer 拉黑了 target；blockedBy：target 拉黑了 viewer。
func (r *Repository) BlockRelation(ctx context.Context, viewerID, targetID int64) (blocked, blockedBy bool, err error) {
	db, err := r.conn()
	if err != nil {
		return false, false, err
	}
	var a, b int
	if err := db.WithContext(ctx).Raw(
		"SELECT COUNT(*) FROM user_block WHERE user_id = ? AND blocked_user_id = ? AND deleted_at IS NULL", viewerID, targetID,
	).Scan(&a).Error; err != nil {
		return false, false, fmt.Errorf("profile: 查询拉黑关系失败: %w", err)
	}
	if err := db.WithContext(ctx).Raw(
		"SELECT COUNT(*) FROM user_block WHERE user_id = ? AND blocked_user_id = ? AND deleted_at IS NULL", targetID, viewerID,
	).Scan(&b).Error; err != nil {
		return false, false, fmt.Errorf("profile: 查询拉黑关系失败: %w", err)
	}
	return a > 0, b > 0, nil
}

// BlockUser 拉黑某人。
// 用 ON DUPLICATE KEY 恢复软删记录，而不是 INSERT IGNORE：
// 后者在记录处于"已软删"状态时会静默失败，用户点了拉黑却没生效。
func (r *Repository) BlockUser(ctx context.Context, uid, target int64) error {
	db, err := r.conn()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Exec(
		`INSERT INTO user_block (user_id, blocked_user_id)
		 VALUES (?, ?)
		 ON DUPLICATE KEY UPDATE deleted_at = NULL`,
		uid, target,
	).Error
}

// UnblockUser 取消拉黑。软删保留痕迹，便于风控识别"反复拉黑又放开"的骚扰模式。
func (r *Repository) UnblockUser(ctx context.Context, uid, target int64) error {
	db, err := r.conn()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Exec(
		`UPDATE user_block SET deleted_at = NOW()
		 WHERE user_id = ? AND blocked_user_id = ? AND deleted_at IS NULL`,
		uid, target,
	).Error
}

// BlockedUser 黑名单条目。
type BlockedUser struct {
	UID       int64  `gorm:"column:uid"`
	Nickname  string `gorm:"column:nickname"`
	Avatar    string `gorm:"column:avatar"`
	CreatedAt string `gorm:"column:created_at"`
}

// ListBlocked 查询我的黑名单（只返回仍生效的）。
func (r *Repository) ListBlocked(ctx context.Context, uid int64, page, size int) ([]BlockedUser, int64, error) {
	db, err := r.conn()
	if err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	var total int64
	if err := db.WithContext(ctx).Table("user_block").
		Where("user_id = ? AND deleted_at IS NULL", uid).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("profile: 统计黑名单失败: %w", err)
	}
	var list []BlockedUser
	err = db.WithContext(ctx).Raw(
		`SELECT u.id AS uid, u.nickname, u.avatar, b.created_at
		 FROM user_block b JOIN user u ON u.id = b.blocked_user_id
		 WHERE b.user_id = ? AND b.deleted_at IS NULL AND u.deleted_at IS NULL
		 ORDER BY b.id DESC LIMIT ? OFFSET ?`,
		uid, size, (page-1)*size,
	).Scan(&list).Error
	if err != nil {
		return nil, 0, fmt.Errorf("profile: 查询黑名单失败: %w", err)
	}
	return list, total, nil
}

// TouchActive 刷新最后活跃时间（心跳接口用，低频写库）。
func (r *Repository) TouchActive(ctx context.Context, uid int64, at time.Time) error {
	db, err := r.conn()
	if err != nil {
		return err
	}
	if err := db.WithContext(ctx).
		Model(&User{}).
		Where("id = ?", uid).
		Update("last_active_at", at).Error; err != nil {
		return fmt.Errorf("profile: 更新活跃时间失败: %w", err)
	}
	return nil
}

// InsertModeration 写一条内容审核记录。
func (r *Repository) InsertModeration(ctx context.Context, bizType int, bizID string, uid int64, scene string, result int, label string, raw *string) error {
	db, err := r.conn()
	if err != nil {
		return err
	}
	rec := &ModerationRecord{
		BizType:  bizType,
		BizID:    bizID,
		UserID:   uid,
		Provider: 1, // 1 本地规则
		Scene:    scene,
		Label:    label,
		Result:   result,
		Raw:      raw,
	}
	if err := db.WithContext(ctx).Create(rec).Error; err != nil {
		return fmt.Errorf("profile: 写审核记录失败: %w", err)
	}
	return nil
}

// FindGlobalPrice 读取全局通话定价。没有配置行时返回零值，由上层决定如何兜底。
func (r *Repository) FindGlobalPrice(ctx context.Context) (*GlobalPrice, error) {
	db, err := r.conn()
	if err != nil {
		return nil, err
	}
	var p GlobalPrice
	err = db.WithContext(ctx).
		Table("call_price_config").
		Select("audio_per_min", "video_per_min", "platform_rate").
		Where("scope = 1 AND status = 1").
		Order("id ASC").
		Limit(1).
		Scan(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("profile: 查询全局定价失败: %w", err)
	}
	return &p, nil
}
