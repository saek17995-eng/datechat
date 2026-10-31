package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/datechat/server/internal/pkg/ctxkey"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/jwt"
	"github.com/datechat/server/internal/pkg/resp"
)

// AdminAuth 后台接口鉴权。
//
// 与用户侧的 Auth 完全分开：
//   - 只接受 TypeAdmin 令牌，用户 access 令牌不能越权访问后台；
//   - Role 必须非空，避免"签发了 admin 类型但没角色"的令牌绕过；
//   - 不校验设备与 Redis 会话（运营会用多台机器登录），到期即重新登录。
func AdminAuth(manager *jwt.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := strings.TrimSpace(c.GetHeader("Authorization"))
		if len(raw) <= len(authPrefix) || !strings.EqualFold(raw[:len(authPrefix)], authPrefix) {
			resp.Fail(c, errs.ErrUnauthorized)
			return
		}
		claims, err := manager.Parse(strings.TrimSpace(raw[len(authPrefix):]))
		if err != nil {
			resp.Fail(c, err)
			return
		}
		if !claims.IsAdmin() {
			resp.Fail(c, errs.ErrForbidden.WithMsg("需要后台管理权限"))
			return
		}
		c.Set(ctxkey.AdminID, claims.UID)
		c.Set(ctxkey.AdminRole, claims.Role)
		c.Next()
	}
}

// CurrentAdminID 取当前后台账号 ID，未登录返回 0。
func CurrentAdminID(c *gin.Context) int64 {
	v, ok := c.Get(ctxkey.AdminID)
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return 0
	}
}

// CurrentAdminName 取当前后台账号名（展示/审计用），缺省回落成 ID 字符串。
func CurrentAdminName(c *gin.Context) string {
	if name, ok := c.Get(ctxkey.AdminName); ok {
		if s, ok2 := name.(string); ok2 && s != "" {
			return s
		}
	}
	return ""
}
