package account

import (
	"context"
	"errors"
	"fmt"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// Repository 是账号模块的数据访问层。
// 简单查询走 GORM；带 "ON DUPLICATE KEY UPDATE" 的写入走原生 SQL——
// 这类语句 GORM 表达不出 MySQL 的语义，硬写反而容易出错。
type Repository struct {
	db *gorm.DB
}

// NewRepository 构造仓储。
func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

// ErrNoDB 表示仓储未注入数据库连接，属于装配错误。
var ErrNoDB = errors.New("account: 数据库未初始化")

func (r *Repository) conn() (*gorm.DB, error) {
	if r == nil || r.db == nil {
		return nil, ErrNoDB
	}
	return r.db, nil
}

// FindByPhoneHMAC 按手机号哈希查用户；不存在返回 (nil, nil)，便于上层区分"新用户"与"数据库错误"。
func (r *Repository) FindByPhoneHMAC(ctx context.Context, hmac string) (*User, error) {
	db, err := r.conn()
	if err != nil {
		return nil, err
	}
	var u User
	err = db.WithContext(ctx).
		Where("phone_hmac = ? AND deleted_at IS NULL", hmac).
		First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("account: 按手机号查询用户失败: %w", err)
	}
	return &u, nil
}

// FindByID 按主键查用户，不存在返回 (nil, nil)。
func (r *Repository) FindByID(ctx context.Context, uid int64) (*User, error) {
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
		return nil, fmt.Errorf("account: 按 ID 查询用户失败: %w", err)
	}
	return &u, nil
}

// CreateUser 在一个事务里创建用户及其关联记录。
//
// 必须同事务的原因：user 有记录但 wallet 没有，会让用户第一次充值时找不到钱包，
// 这类"半条命"数据线上极难排查。任一步失败整体回滚。
func (r *Repository) CreateUser(ctx context.Context, u *User) error {
	db, err := r.conn()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(u).Error; err != nil {
			return err
		}

		// IM 账号依赖自增主键，只能插入后回填。
		imUserID := IMUserIDOf(u.ID)
		if err := tx.Model(&User{}).Where("id = ?", u.ID).Update("im_user_id", imUserID).Error; err != nil {
			return fmt.Errorf("account: 回填 im_user_id 失败: %w", err)
		}
		u.IMUserID = &imUserID

		auth := UserAuth{
			UserID:     u.ID,
			AuthType:   AuthTypePhone,
			Identifier: u.PhoneHMAC,
			Status:     1,
		}
		if err := tx.Create(&auth).Error; err != nil {
			return fmt.Errorf("account: 创建登录方式失败: %w", err)
		}

		// user_profile 与 wallet 只有 user_id 必填，其余取默认值。
		if err := tx.Exec("INSERT IGNORE INTO user_profile (user_id) VALUES (?)", u.ID).Error; err != nil {
			return fmt.Errorf("account: 初始化用户资料失败: %w", err)
		}
		if err := tx.Exec("INSERT IGNORE INTO wallet (user_id) VALUES (?)", u.ID).Error; err != nil {
			return fmt.Errorf("account: 初始化钱包失败: %w", err)
		}
		return nil
	})
}

// UpsertDevice 登记设备与推送令牌。
// 同一 (user_id, device_id) 只保留一行：换设备登录不制造垃圾数据，重装后令牌会被覆盖。
func (r *Repository) UpsertDevice(ctx context.Context, d *UserDevice) error {
	db, err := r.conn()
	if err != nil {
		return err
	}
	const q = `INSERT INTO user_device
		(user_id, device_id, platform, push_token, app_version, last_ip, last_active_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			platform = VALUES(platform),
			push_token = VALUES(push_token),
			app_version = VALUES(app_version),
			last_ip = VALUES(last_ip),
			last_active_at = VALUES(last_active_at)`
	if err := db.WithContext(ctx).Exec(q,
		d.UserID, d.DeviceID, d.Platform, d.PushToken, d.AppVersion, d.LastIP, d.LastActiveAt,
	).Error; err != nil {
		return fmt.Errorf("account: 登记设备失败: %w", err)
	}
	return nil
}

// InsertLoginLog 写登录审计日志。失败不应阻断登录流程，由调用方决定是否只记日志。
func (r *Repository) InsertLoginLog(ctx context.Context, l *UserLoginLog) error {
	db, err := r.conn()
	if err != nil {
		return err
	}
	if err := db.WithContext(ctx).Create(l).Error; err != nil {
		return fmt.Errorf("account: 写登录日志失败: %w", err)
	}
	return nil
}

// InsertSMSSendLog 写验证码发送审计。失败不影响主流程，由调用方仅记日志。
func (r *Repository) InsertSMSSendLog(ctx context.Context, l *SMSSendLog) error {
	db, err := r.conn()
	if err != nil {
		return err
	}
	if err := db.WithContext(ctx).Create(l).Error; err != nil {
		return fmt.Errorf("account: 写短信发送日志失败: %w", err)
	}
	return nil
}

// TouchActive 刷新最后活跃时间。
func (r *Repository) TouchActive(ctx context.Context, uid int64, at time.Time) error {
	db, err := r.conn()
	if err != nil {
		return err
	}
	if err := db.WithContext(ctx).
		Model(&User{}).
		Where("id = ?", uid).
		Update("last_active_at", at).Error; err != nil {
		return fmt.Errorf("account: 更新活跃时间失败: %w", err)
	}
	return nil
}

// FindProfile 查用户扩展资料，不存在返回 (nil, nil)。
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
		return nil, fmt.Errorf("account: 查询用户资料失败: %w", err)
	}
	return &p, nil
}

// IsDuplicateKey 判断是否为唯一索引冲突（MySQL 1062）。
// 注册与设备写入都存在并发场景，必须靠唯一索引兜底而不是"先查再插"。
func IsDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	var me *driver.MySQLError
	if errors.As(err, &me) {
		return me.Number == 1062
	}
	return false
}
