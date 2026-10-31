// Package ctxkey 定义 gin.Context 中共享的键名。
// 统一收口可避免各模块随手写字符串导致取值失败。
package ctxkey

const (
	// TraceID 请求链路 ID，贯穿日志与响应体。
	TraceID = "ctx.trace_id"
	// UID 登录用户 ID（JWT 中间件写入）。
	UID = "ctx.uid"
	// DeviceID 登录设备标识（JWT 中间件写入）。
	DeviceID = "ctx.device_id"
	// AdminID 后台管理员 ID（后台鉴权中间件写入）。
	AdminID = "ctx.admin_id"
)
