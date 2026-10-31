package realname

import (
	"context"
	"errors"
	"fmt"

	driver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// Repository 实名数据访问。
type Repository struct {
	db *gorm.DB
}

// NewRepository 构造仓储。
func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

// FindByUserID 按用户查实名记录，不存在返回 (nil, nil)。
func (r *Repository) FindByUserID(ctx context.Context, uid int64) (*Record, error) {
	var rec Record
	err := r.db.WithContext(ctx).Where("user_id = ?", uid).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("realname: 查询实名记录失败: %w", err)
	}
	return &rec, nil
}

// FindByIDCardHMAC 按身份证哈希查记录，用于"一号一人"查重。
func (r *Repository) FindByIDCardHMAC(ctx context.Context, hmac string) (*Record, error) {
	var rec Record
	err := r.db.WithContext(ctx).Where("id_card_hmac = ?", hmac).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("realname: 按身份证查询实名记录失败: %w", err)
	}
	return &rec, nil
}

// Save 写入或更新实名记录。
//
// 刻意不用 "INSERT ... ON DUPLICATE KEY UPDATE"：user_realname 上有 user_id 与 id_card_hmac
// 两个唯一键，一旦命中 id_card 冲突，MySQL 会去更新"另一个用户"的行——这是数据事故。
// 因此先按 user_id 判断存在性，再由唯一索引把身份证冲突暴露成 1062 错误。
func (r *Repository) Save(ctx context.Context, rec *Record) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&Record{}).Where("user_id = ?", rec.UserID).Count(&count).Error; err != nil {
			return fmt.Errorf("realname: 统计实名记录失败: %w", err)
		}
		if count == 0 {
			if err := tx.Create(rec).Error; err != nil {
				return err
			}
			return nil
		}
		return tx.Model(&Record{}).
			Where("user_id = ?", rec.UserID).
			Updates(map[string]any{
				"real_name_cipher": rec.RealNameCipher,
				"id_card_hmac":     rec.IDCardHMAC,
				"id_card_cipher":   rec.IDCardCipher,
				"status":           rec.Status,
				"channel":          rec.Channel,
				"provider_trace":   rec.ProviderTrace,
				"fail_reason":      rec.FailReason,
				"verified_at":      rec.VerifiedAt,
			}).Error
	})
}

// UpdateProfileStatus 同步 user_profile.realname_status 冗余字段。
// 冗余是有必要的：附近人列表/主页只要一次查询就能判断是否实名，不用 join。
func (r *Repository) UpdateProfileStatus(ctx context.Context, uid int64, status int) error {
	if err := r.db.WithContext(ctx).
		Exec("UPDATE user_profile SET realname_status = ? WHERE user_id = ?", status, uid).
		Error; err != nil {
		return fmt.Errorf("realname: 同步实名状态失败: %w", err)
	}
	return nil
}

// IsDuplicateKey 判断唯一索引冲突（MySQL 1062）。
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
