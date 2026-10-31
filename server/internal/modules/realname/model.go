package realname

import "time"

// 实名状态。与 user_realname.status、user_profile.realname_status 的取值保持一致，
// 这样两处状态永远同步，不会出现"实名表已通过、资料表还显示未认证"的错位。
const (
	StatusUnverified = 0
	StatusProcessing = 1
	StatusPassed     = 2
	StatusRejected   = 3
)

// 核身渠道，与 user_realname.channel 对齐。
const (
	ChannelMock    = 0
	ChannelTencent = 1
	ChannelAliyun  = 2
	ChannelManual  = 3
)

// Record 映射 user_realname 表。
type Record struct {
	ID             int64      `gorm:"column:id;primaryKey;autoIncrement"`
	UserID         int64      `gorm:"column:user_id"`
	RealNameCipher []byte     `gorm:"column:real_name_cipher"`
	IDCardHMAC     string     `gorm:"column:id_card_hmac"`
	IDCardCipher   []byte     `gorm:"column:id_card_cipher"`
	Status         int        `gorm:"column:status"`
	Channel        int        `gorm:"column:channel"`
	ProviderTrace  string     `gorm:"column:provider_trace"`
	FailReason     string     `gorm:"column:fail_reason"`
	VerifiedAt     *time.Time `gorm:"column:verified_at"`
	CreatedAt      time.Time  `gorm:"column:created_at"`
	UpdatedAt      time.Time  `gorm:"column:updated_at"`
}

// TableName 固定表名。
func (Record) TableName() string { return "user_realname" }
