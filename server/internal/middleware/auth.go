package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/datechat/server/internal/pkg/ctxkey"
	"github.com/datechat/server/internal/pkg/errs"
	"github.com/datechat/server/internal/pkg/jwt"
	"github.com/datechat/server/internal/pkg/resp"
)

const authPrefix = "Bearer "

// Auth 校验 Authorization 头中的 access 令牌，并把 UID 写入上下文。
// 放在需要登录的路由组上，公开接口不要挂。
func Auth(manager *jwt.Manager) gin.HandlerFunc {
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
		if claims.Type != jwt.TypeAccess {
			resp.Fail(c, errs.ErrTokenInvalid.WithMsg("令牌类型不正确"))
			return
		}

		c.Set(ctxkey.UID, claims.UID)
		c.Set(ctxkey.DeviceID, claims.DeviceID)
		c.Next()
	}
}

// CurrentUID 取当前登录用户 ID，未登录返回 0。
func CurrentUID(c *gin.Context) int64 {
	value, ok := c.Get(ctxkey.UID)
	if !ok {
		return 0
	}
	uid, _ := value.(int64)
	return uid
}

// MustUID 供已挂 Auth 的 handler 使用，取不到即为编程错误。
func MustUID(c *gin.Context) int64 {
	uid := CurrentUID(c)
	if uid <= 0 {
		// 说明路由忘了挂 Auth，属于代码缺陷，应当尽早暴露。
		panic("middleware.MustUID: 当前请求没有登录用户，请检查路由是否挂载了 Auth 中间件")
	}
	return uid
}

// CurrentDeviceID 取当前设备标识。
func CurrentDeviceID(c *gin.Context) string {
	value, ok := c.Get(ctxkey.DeviceID)
	if !ok {
		return ""
	}
	deviceID, _ := value.(string)
	return deviceID
}
