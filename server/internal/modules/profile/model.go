package profile

import (
	"database/sql/driver"
	"encoding/json"
	"time"
)

// 上传场景，与 upload_file.scene 对齐。
const (
	SceneAvatar   = 1 // 头像
	ScenePhoto    = 2 // 相册
	SceneVoice    = 3 // 语音自我介绍
	SceneEvidence = 4 // 举报证据
)

// 存储通道，与 upload_file.storage 对齐。
const (
	StorageLocal = 1 // 本地盘（自测/内网）
	StorageCOS   = 2 // 腾讯云对象存储
)

// 文件审核状态，与 upload_file.status 对齐。
const (
	FileStatusPending  = 1 // 待审核
	FileStatusPassed   = 2 // 已通过
	FileStatusRejected = 3 // 已拒绝
)

// 性别，与 user.gender 对齐。
const (
	GenderUnknown = 0
	GenderMale    = 1
	GenderFemale  = 2
)

// 用户状态，与 user.status 对齐。
const (
	StatusNormal = 1
)

// City 城市字典项。
type City struct {
	Code     string  `gorm:"column:city_code"`
	Province string  `gorm:"column:province"`
	Name     string  `gorm:"column:city"`
	Lng      float64 `gorm:"column:lng"`
	Lat      float64 `gorm:"column:lat"`
	Sort     int     `gorm:"column:sort"`
}

// TableName 固定表名。
func (City) TableName() string { return "region_city" }

// User 是 profile 模块需要的用户字段。
// 不复用 account.User：领域之间共享结构体会让"改一个字段影响另一个模块"变成常态，
// 这里只取主页与资料编辑真正需要的列。
type User struct {
	ID           int64      `gorm:"column:id;primaryKey"`
	Nickname     string     `gorm:"column:nickname"`
	Avatar       string     `gorm:"column:avatar"`
	Gender       int        `gorm:"column:gender"`
	Birthday     *time.Time `gorm:"column:birthday"`
	CityCode     string     `gorm:"column:city_code"`
	Status       int        `gorm:"column:status"`
	LastActiveAt *time.Time `gorm:"column:last_active_at"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at"`
}

// TableName 固定表名。
func (User) TableName() string { return "user" }

// StringList 是 JSON 数组列（标签、相册）的读写适配器。
//
// 空列表统一序列化成 [] 而不是 NULL：客户端拿到 null 要到处判空，
// 而 "没有标签" 与 "标签字段缺失" 在业务上是同一件事。
type StringList []string

// Value 实现 driver.Valuer。
func (s StringList) Value() (driver.Value, error) {
	if s == nil {
		return "[]", nil
	}
	b, err := json.Marshal([]string(s))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan 实现 sql.Scanner。
func (s *StringList) Scan(v any) error {
	if v == nil {
		*s = StringList{}
		return nil
	}
	var raw []byte
	switch t := v.(type) {
	case []byte:
		raw = t
	case string:
		raw = []byte(t)
	default:
		*s = StringList{}
		return nil
	}
	if len(raw) == 0 {
		*s = StringList{}
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	if out == nil {
		out = []string{}
	}
	*s = out
	return nil
}

// UserProfile 映射 user_profile 表。
type UserProfile struct {
	UserID         int64      `gorm:"column:user_id;primaryKey"`
	Height         int        `gorm:"column:height"`
	Job            string     `gorm:"column:job"`
	Tags           StringList `gorm:"column:tags;type:json"`
	Photos         StringList `gorm:"column:photos;type:json"`
	VoiceIntro     string     `gorm:"column:voice_intro"`
	Bio            string     `gorm:"column:bio"`
	PriceAudio     int        `gorm:"column:price_audio"`
	PriceVideo     int        `gorm:"column:price_video"`
	PriceEnabled   int        `gorm:"column:price_enabled"`
	RealnameStatus int        `gorm:"column:realname_status"`
	CreatedAt      time.Time  `gorm:"column:created_at"`
	UpdatedAt      time.Time  `gorm:"column:updated_at"`
}

// TableName 固定表名。
func (UserProfile) TableName() string { return "user_profile" }

// UploadFile 映射 upload_file 表：上传台账。
type UploadFile struct {
	ID        int64      `gorm:"column:id;primaryKey;autoIncrement"`
	UserID    int64      `gorm:"column:user_id"`
	Scene     int        `gorm:"column:scene"`
	Storage   int        `gorm:"column:storage"`
	URL       string     `gorm:"column:url"`
	ObjectKey string     `gorm:"column:object_key"`
	FileSize  int64      `gorm:"column:file_size"`
	MimeType  string     `gorm:"column:mime_type"`
	Width     int        `gorm:"column:width"`
	Height    int        `gorm:"column:height"`
	Checksum  string     `gorm:"column:checksum"`
	Status    int        `gorm:"column:status"`
	Used      int        `gorm:"column:used"`
	CreatedAt time.Time  `gorm:"column:created_at"`
	UpdatedAt *time.Time `gorm:"column:updated_at"`
}

// TableName 固定表名。
func (UploadFile) TableName() string { return "upload_file" }

// UserVisit 映射 user_visit 表：谁看过我。
type UserVisit struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement"`
	VisitorID int64     `gorm:"column:visitor_id"`
	TargetID  int64     `gorm:"column:target_id"`
	VisitedAt time.Time `gorm:"column:visited_at"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

// TableName 固定表名。
func (UserVisit) TableName() string { return "user_visit" }

// GlobalPrice 全局通话定价（call_price_config 的 scope=1 行）。
type GlobalPrice struct {
	AudioPerMin  int `gorm:"column:audio_per_min"`
	VideoPerMin  int `gorm:"column:video_per_min"`
	PlatformRate int `gorm:"column:platform_rate"`
}

// ModerationRecord 映射 moderation_record 表：内容审核留痕。
//
// P2 只做本地规则的结果落库（敏感词命中、头像待复核），
// P4 接入腾讯云 TMS / 图片审核后，同一个表承接第三方返回的 label 与 score。
type ModerationRecord struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement"`
	BizType   int       `gorm:"column:biz_type"`
	BizID     string    `gorm:"column:biz_id"`
	UserID    int64     `gorm:"column:user_id"`
	Provider  int       `gorm:"column:provider"`
	Scene     string    `gorm:"column:scene"`
	Label     string    `gorm:"column:label"`
	Result    int       `gorm:"column:result"`
	Raw       *string   `gorm:"column:raw;type:json"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

// TableName 固定表名。
func (ModerationRecord) TableName() string { return "moderation_record" }
