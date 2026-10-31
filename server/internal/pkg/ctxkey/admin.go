package ctxkey

// 后台鉴权相关上下文键。
const (
	// AdminName 后台账号名，审计日志展示用。
	AdminName = "ctx.admin_name"
	// AdminRole 后台角色：admin / auditor / operator。
	AdminRole = "ctx.admin_role"
)
