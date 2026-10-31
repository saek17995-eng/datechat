package chat

import (
	"encoding/json"
	"time"
)

// 消息类型，与 message.msg_type 对齐。
const (
	MessageTypeText   = 1
	MessageTypeImage  = 2
	MessageTypeAudio  = 3
	MessageTypeVideo  = 4
	MessageTypeCall   = 5
	MessageTypeSystem = 6
)

// 消息状态，与 message.status 对齐。
const (
	MessageStatusNormal   = 1
	MessageStatusRecalled = 2
	MessageStatusDeleted  = 3
)

// User 是聊天模块最小用户视图。
type User struct {
	ID       int64  `gorm:"column:id;primaryKey"`
	Nickname string `gorm:"column:nickname"`
	Avatar   string `gorm:"column:avatar"`
	Status   int    `gorm:"column:status"`
}

func (User) TableName() string { return "user" }

// Conversation 单聊会话。
type Conversation struct {
	ID             int64      `gorm:"column:id;primaryKey;autoIncrement"`
	UserAID        int64      `gorm:"column:user_a_id"`
	UserBID        int64      `gorm:"column:user_b_id"`
	LastMsgID      int64      `gorm:"column:last_msg_id"`
	LastMsgType    int        `gorm:"column:last_msg_type"`
	LastMsgPreview string     `gorm:"column:last_msg_preview"`
	LastMsgAt      *time.Time `gorm:"column:last_msg_at"`
	MsgCount       int        `gorm:"column:msg_count"`
	CreatedAt      time.Time  `gorm:"column:created_at"`
	UpdatedAt      time.Time  `gorm:"column:updated_at"`
}

func (Conversation) TableName() string { return "conversation" }

// Message 是腾讯 IM 单聊消息的本地镜像。
type Message struct {
	ID          int64           `gorm:"column:id;primaryKey;autoIncrement"`
	MsgID       string          `gorm:"column:msg_id"`
	MsgKey      string          `gorm:"column:msg_key"`
	ClientMsgID *string         `gorm:"column:client_msg_id"`
	ConvID      int64           `gorm:"column:conv_id"`
	FromUID     int64           `gorm:"column:from_uid"`
	ToUID       int64           `gorm:"column:to_uid"`
	MsgType     int             `gorm:"column:msg_type"`
	Content     json.RawMessage `gorm:"column:content;type:json"`
	Seq         int64           `gorm:"column:seq"`
	Status      int             `gorm:"column:status"`
	SentAt      time.Time       `gorm:"column:sent_at"`
	RecalledAt  *time.Time      `gorm:"column:recalled_at"`
	RecalledBy  int64           `gorm:"column:recalled_by"`
	CreatedAt   time.Time       `gorm:"column:created_at"`
	UpdatedAt   time.Time       `gorm:"column:updated_at"`
}

func (Message) TableName() string { return "message" }

// CallbackLog 是 IM 回调幂等与审计记录。
type CallbackLog struct {
	ID           int64      `gorm:"column:id;primaryKey;autoIncrement"`
	SdkAppID     int64      `gorm:"column:sdk_app_id"`
	CallbackType string     `gorm:"column:callback_type"`
	Command      string     `gorm:"column:command"`
	PayloadHash  string     `gorm:"column:payload_hash"`
	Raw          string     `gorm:"column:raw;type:json"`
	Processed    int        `gorm:"column:processed"`
	RetryCount   int        `gorm:"column:retry_count"`
	ErrorMsg     string     `gorm:"column:error_msg"`
	ProcessedAt  *time.Time `gorm:"column:processed_at"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at"`
}

func (CallbackLog) TableName() string { return "im_callback_log" }

// ModerationRecord 复用全站内容审核记录表。
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

func (ModerationRecord) TableName() string { return "moderation_record" }
