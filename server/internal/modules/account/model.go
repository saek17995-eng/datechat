package account

import (
	"strconv"
	"time"
)

// 用户状态，与 user.status 列注释一致。
const (
	UserStatusNormal  = 1
	UserStatusFrozen  = 2
	UserStatusBanned  = 3
	UserStatusDeleted = 4
)

// 登录方式，与 user_auth.auth_type 对齐。
const (
	AuthTypePhone = 1
)

// 客户端平台，与 user_device.platform 对齐。
const (
	PlatformUnknown = 0
	PlatformIOS     = 1
	PlatformAndroid = 2
	PlatformWeb     = 3
)

// 登录日志结果。
const (
	LoginResultSuccess = 1
	LoginResultFailed  = 2
)

// User 映射 user 表。
// 敏感字段一律以密文形式存在，PhoneHMAC 用于唯一索引与查询。
type User struct {
	ID           int64      `gorm:"column:id;primaryKey;autoIncrement"`
	PhoneHMAC    string     `gorm:"column:phone_hmac"`
	PhoneCipher  []byte     `gorm:"column:phone_cipher"`
	Nickname     string     `gorm:"column:nickname"`
	Avatar       string     `gorm:"column:avatar"`
	Gender       int        `gorm:"column:gender"`
	Birthday     *time.Time `gorm:"column:birthday"`
	CityCode     string     `gorm:"column:city_code"`
	IMUserID     *string    `gorm:"column:im_user_id"`
	Status       int        `gorm:"column:status"`
	RegisterIP   string     `gorm:"column:register_ip"`
	LastActiveAt *time.Time `gorm:"column:last_active_at"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at"`
	DeletedAt    *time.Time `gorm:"column:deleted_at"`
}

// TableName 固定表名，避免 GORM 复数化猜测。
func (User) TableName() string { return "user" }

// UserAuth 映射 user_auth 表。
type UserAuth struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement"`
	UserID     int64     `gorm:"column:user_id"`
	AuthType   int       `gorm:"column:auth_type"`
	Identifier string    `gorm:"column:identifier"`
	Credential string    `gorm:"column:credential"`
	Status     int       `gorm:"column:status"`
	CreatedAt  time.Time `gorm:"column:created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at"`
}

// TableName 固定表名。
func (UserAuth) TableName() string { return "user_auth" }

// UserProfile 映射 user_profile 表（主键即 user_id）。
type UserProfile struct {
	UserID         int64     `gorm:"column:user_id;primaryKey"`
	Height         int       `gorm:"column:height"`
	Job            string    `gorm:"column:job"`
	Bio            string    `gorm:"column:bio"`
	PriceAudio     int       `gorm:"column:price_audio"`
	PriceVideo     int       `gorm:"column:price_video"`
	PriceEnabled   int       `gorm:"column:price_enabled"`
	RealnameStatus int       `gorm:"column:realname_status"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
}

// TableName 固定表名。
func (UserProfile) TableName() string { return "user_profile" }

// UserDevice 映射 user_device 表。
type UserDevice struct {
	ID           int64      `gorm:"column:id;primaryKey;autoIncrement"`
	UserID       int64      `gorm:"column:user_id"`
	DeviceID     string     `gorm:"column:device_id"`
	Platform     int        `gorm:"column:platform"`
	PushToken    string     `gorm:"column:push_token"`
	AppVersion   string     `gorm:"column:app_version"`
	LastIP       string     `gorm:"column:last_ip"`
	LastActiveAt *time.Time `gorm:"column:last_active_at"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at"`
}

// TableName 固定表名。
func (UserDevice) TableName() string { return "user_device" }

// UserLoginLog 映射 user_login_log 表。
type UserLoginLog struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement"`
	UserID    int64     `gorm:"column:user_id"`
	PhoneHMAC string    `gorm:"column:phone_hmac"`
	DeviceID  string    `gorm:"column:device_id"`
	IP        string    `gorm:"column:ip"`
	UserAgent string    `gorm:"column:user_agent"`
	Result    int       `gorm:"column:result"`
	Reason    string    `gorm:"column:reason"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

// TableName 固定表名。
func (UserLoginLog) TableName() string { return "user_login_log" }

// SMSSendLog 映射 sms_send_log 表：验证码发送审计。
// 独立成表而不是塞进日志系统，是因为要按手机号做频控复盘和短信成本核算。
type SMSSendLog struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement"`
	PhoneHMAC string    `gorm:"column:phone_hmac"`
	PhoneMask string    `gorm:"column:phone_mask"`
	Scene     string    `gorm:"column:scene"`
	Provider  string    `gorm:"column:provider"`
	RequestID string    `gorm:"column:request_id"`
	IP        string    `gorm:"column:ip"`
	Result    int       `gorm:"column:result"`
	ErrorMsg  string    `gorm:"column:error_msg"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

// TableName 固定表名。
func (SMSSendLog) TableName() string { return "sms_send_log" }

// IMUserIDOf 生成 IM 账号，与用户主键同构，避免额外映射表。
// 客户端与服务端都用这一个规则推导，任何一方都不需要查询映射关系。
func IMUserIDOf(uid int64) string {
	return "u_" + strconv.FormatInt(uid, 10)
}
