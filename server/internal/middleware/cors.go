package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/datechat/server/internal/config"
)

// CORS 处理跨域与预检请求。
// 移动端 App 本身不受同源策略限制，但 Flutter Web 调试、运营后台都需要。
func CORS(cfg config.CORSConfig) gin.HandlerFunc {
	allowAll := false
	allowed := make(map[string]struct{}, len(cfg.AllowOrigins))
	for _, origin := range cfg.AllowOrigins {
		if origin == "*" {
			allowAll = true
		}
		allowed[origin] = struct{}{}
	}

	methods := strings.Join(cfg.AllowMethods, ", ")
	if methods == "" {
		methods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"
	}
	headers := strings.Join(cfg.AllowHeaders, ", ")
	if headers == "" {
		headers = "Origin, Content-Type, Authorization, X-Request-Id"
	}
	expose := strings.Join(cfg.ExposeHeaders, ", ")
	maxAge := strconv.Itoa(int(cfg.MaxAge.D().Seconds()))

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			if allowAll {
				c.Header("Access-Control-Allow-Origin", "*")
			} else if _, ok := allowed[origin]; ok {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Vary", "Origin")
			}
		}

		if c.Request.Method == http.MethodOptions {
			c.Header("Access-Control-Allow-Methods", methods)
			c.Header("Access-Control-Allow-Headers", headers)
			if expose != "" {
				c.Header("Access-Control-Expose-Headers", expose)
			}
			if maxAge != "0" {
				c.Header("Access-Control-Max-Age", maxAge)
			}
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
