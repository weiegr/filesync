package handler

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// StaticHandler 前端静态资源（由 main 包 embed 后注入）
type StaticHandler struct {
	root fs.FS
}

// NewStaticHandlerFS 从任意 fs.FS 构造静态资源处理器
func NewStaticHandlerFS(root fs.FS) (*StaticHandler, error) {
	return &StaticHandler{root: root}, nil
}

// Index GET /  入口页
func (h *StaticHandler) Index(c *gin.Context) {
	h.serveFile(c, "index.html")
}

// Upload GET /upload  上传页
func (h *StaticHandler) Upload(c *gin.Context) {
	h.serveFile(c, "upload.html")
}

// ShareList GET /s/:code  下载页
func (h *StaticHandler) ShareList(c *gin.Context) {
	h.serveFile(c, "share-list.html")
}

// Asset GET /vendor/*  静态资源（图标字体等）
func (h *StaticHandler) Asset(c *gin.Context) {
	// gin 的 *filepath 通配符捕获路由前缀之后的剩余(含前导 /)
	// 例如 /vendor/phosphor/style.css -> filepath = "/phosphor/style.css"
	// 需补回 vendor 前缀
	rel := strings.TrimPrefix(c.Param("filepath"), "/")
	filePath := "vendor/" + rel
	h.serveFile(c, filePath)
}

// serveFile 从 fs 中读取并写回
func (h *StaticHandler) serveFile(c *gin.Context, name string) {
	// 拒绝路径穿越
	if strings.Contains(name, "..") {
		c.String(http.StatusBadRequest, "非法路径")
		return
	}

	data, err := fs.ReadFile(h.root, name)
	if err != nil {
		c.String(http.StatusNotFound, "资源不存在")
		return
	}

	ct := "application/octet-stream"
	switch {
	case strings.HasSuffix(name, ".html"):
		ct = "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		ct = "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		ct = "application/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".woff2"):
		ct = "font/woff2"
	case strings.HasSuffix(name, ".woff"):
		ct = "font/woff"
	case strings.HasSuffix(name, ".ttf"):
		ct = "font/ttf"
	case strings.HasSuffix(name, ".svg"):
		ct = "image/svg+xml"
	case strings.HasSuffix(name, ".jpg"), strings.HasSuffix(name, ".jpeg"):
		ct = "image/jpeg"
	case strings.HasSuffix(name, ".png"):
		ct = "image/png"
	case strings.HasSuffix(name, ".webp"):
		ct = "image/webp"
	case strings.HasSuffix(name, ".gif"):
		ct = "image/gif"
	}
	c.Data(http.StatusOK, ct, data)
}
