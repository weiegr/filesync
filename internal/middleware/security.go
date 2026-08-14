package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// SecurityHeaders 设置安全响应头
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 防点击劫持
		c.Header("X-Frame-Options", "DENY")
		// 防 MIME 嗅探
		c.Header("X-Content-Type-Options", "nosniff")
		// 控制 Referrer 信息泄露
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		// 内容安全策略
		// Phosphor 图标已本地化(/vendor)，仅 Google Fonts (Inter 正文) 仍走 CDN
		c.Header("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com; "+
				"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; "+
				"font-src 'self' https://fonts.gstatic.com data:; "+
				"img-src 'self' data: https://images.unsplash.com; "+
				"connect-src 'self'; "+
				"frame-ancestors 'none'; "+
				"base-uri 'self'; "+
				"form-action 'self'")
		// 仅对页面加 HSTS（API 也可加，但 nginx 层会统一加，这里设短一些兜底）
		if c.Request.TLS != nil {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		// 隐藏服务器版本
		c.Header("Server", "filesync")
		c.Header("X-Powered-By", "")
		c.Next()
	}
}

// NoSniff 静态资源 contentType 防嗅探已通过 X-Content-Type-Options，这里占位便于将来扩展
func NoSniff() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Next()
	}
}

// Options 预检请求快速返回
func Options() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodOptions {
			c.Header("Access-Control-Allow-Origin", "'self'")
			c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, X-Download-Token")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
