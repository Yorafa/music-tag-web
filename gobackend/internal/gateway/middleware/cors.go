package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"go-music-tag/internal/config"
)

// CORS 在 cfg.CORSAllowedOrigins 白名单基础上回显 Access-Control-Allow-Origin。
//
// 与 P1 反射所有 Origin 的行为不同，本实现：
//   - 当 cfg.CORSAllowedOrigins 为空 (默认) 时不回显 Allow-Origin —— 浏览器
//     会把跨域请求拒在 client 侧；
//   - 仅当 Origin 命中白名单才回显 + 携带 Credentials:true + Vary:Origin，
//     避免缓存污染；
//   - OPTIONS preflight 始终返回 204，让受信任前端的 CORS 预检能通过。
//
// 为 CORS_ALLOWED_ORIGINS 推荐配置：
//
//	CORS_ALLOWED_ORIGINS=https://music.example.com,http://localhost:8080
//
// cfg 为 nil 时退化为 "no CORS" —— 不挂 Allow-Origin / Allow-Credentials 但
// 仍放行 OPTIONS preflight 与 Methods/Headers，便于在测试 rig 或调用方立刻
// 跳过配置的场景下不直接 panic。
func CORS(cfg *config.Config) gin.HandlerFunc {
	if cfg == nil {
		return func(c *gin.Context) {
			if c.Request.Method == http.MethodOptions {
				c.Header("Cache-Control", "no-store")
				c.AbortWithStatus(http.StatusNoContent)
				return
			}
			c.Next()
		}
	}
	whitelist := make(map[string]bool, len(cfg.CORSAllowedOrigins))
	for _, o := range cfg.CORSAllowedOrigins {
		whitelist[o] = true
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && whitelist[origin] {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type,Authorization")
		c.Header("Access-Control-Expose-Headers", "Content-Disposition")

		if c.Request.Method == http.MethodOptions {
			// No-store ensures browsers don't cache the preflight. If the
			// operator rotates CORS_ALLOWED_ORIGINS, stale preflights
			// could otherwise persist on intermediaries and leak to
			// origins that just got demoted.
			c.Header("Cache-Control", "no-store")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
