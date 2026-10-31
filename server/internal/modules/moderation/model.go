package moderation

import (
	"context"
	"time"

	"github.com/datechat/server/internal/pkg/textfilter"
	"github.com/datechat/server/internal/pkg/tms"
)

// BizType 审核对象类型，与 moderation_record.biz_type 对齐。
const (
	BizMessage = 1 // 消息
	BizProfile = 2 // 资料（昵称/签名）
	BizAvatar  = 3 // 头像
	BizAudio   = 4 // 语音
	BizVideo   = 5 // 视频帧
)

// Provider 审核来源，与 moderation_record.provider 对齐。
const (
	ProviderDFA   = 1 // 本地 DFA
	ProviderTMS   = 2 // 腾讯云文本审核
	ProviderTRTC  = 3 // TRTC 截图
	ProviderHuman = 4 // 人工
)

// Result 审核结论，与 moderation_record.result 对齐。
const (
	ResultPass   = 1 // 通过
	ResultReject = 2 // 拒绝
	ResultReview = 3 // 转人工
)

// Verdict 一次审核的统一结论。
// 上层只关心"放行/拦截/转人工"，不关心结论来自本地还是云端。
type Verdict struct {
	Blocked  bool    // 是否拦截（拒绝提交）
	Review   bool    // 是否需要人工复核（放行但留痕）
	Text     string  // 处理后的文本（替换级会打码）
	Label    string  // 命中标签：porn/ad/abuse/…
	Score    float64 // 置信度 0-1
	Provider int     // 结论来源：ProviderDFA / ProviderTMS
	Hits     []textfilter.Hit
}

// TextFilter 本地过滤能力的最小接口，便于测试替换。
type TextFilter interface {
	Check(text string) textfilter.Result
}

// CloudChecker 云端审核能力的最小接口。
type CloudChecker interface {
	Enabled() bool
	Moderate(ctx context.Context, text, dataID string, uid int64, nickname string) (*tms.Result, error)
}

// Report 用户举报。
type Report struct {
	ID           int64
	ReporterID   int64
	TargetType   int
	TargetID     string
	TargetUID    int64
	Reason       string
	Detail       string
	Evidence     *string // JSON 数组，无证据时为 NULL（JSON 列不接受空串）
	Status       int
	HandlerID    int64
	HandleResult string
	HandleNote   string
	HandledAt    *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// 举报对象类型。
const (
	TargetUser    = 1
	TargetMessage = 2
	TargetCall    = 3
	TargetAvatar  = 4
)

// 举报状态。
const (
	ReportPending  = 1
	ReportHandled  = 2
	ReportRejected = 3
)

// 处置动作，写 admin_op_log.action。
const (
	ActionWarn    = "warn"
	ActionFreeze  = "freeze"
	ActionBan     = "ban"
	ActionUnban   = "unban"
	ActionIgnore  = "ignore"
	ActionReviewP = "review_pass"
	ActionReviewR = "review_reject"
)

// AdminUser 后台账号。表 admin_user 由 P0 建表，role 是 TINYINT 枚举。
type AdminUser struct {
	ID           int64
	Username     string
	PasswordHash string
	RealName     string
	Role         int // RoleSuperAdmin / RoleOperator / RoleAuditor / RoleSupport
	Status       int
	LastLoginAt  *time.Time
	LastLoginIP  string
}

// 后台角色，与 admin_user.role 注释一致。
const (
	RoleSuperAdmin = 1
	RoleOperator   = 2
	RoleAuditor    = 3
	RoleSupport    = 4
)

// RoleName 把角色枚举转成可读名，用于 JWT 载荷与审计展示。
func RoleName(role int) string {
	switch role {
	case RoleSuperAdmin:
		return "super_admin"
	case RoleOperator:
		return "operator"
	case RoleAuditor:
		return "auditor"
	case RoleSupport:
		return "support"
	default:
		return ""
	}
}

const (
	AdminStatusEnabled  = 1
	AdminStatusDisabled = 2
)

// 用户状态，与 user.status 对齐。
const (
	UserStatusNormal  = 1
	UserStatusFrozen  = 2
	UserStatusBanned  = 3
	UserStatusDeleted = 4
)

// ModerationRecord 审核留痕，后台复审列表用。
type ModerationRecord struct {
	ID         int64
	BizType    int
	BizID      string
	UserID     int64
	Provider   int
	Scene      string
	Label      string
	Score      float64
	Result     int
	Raw        string
	HandledBy  int64
	HandledAt  *time.Time
	HandleNote string
	CreatedAt  time.Time
}
