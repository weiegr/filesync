package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"filesync/internal/config"
	"filesync/internal/middleware"
	"filesync/internal/service"
	"filesync/internal/store"

	"github.com/gin-gonic/gin"
)

type ShareHandler struct {
	svc     *service.ShareService
	cfg     *config.Config
	attempt *middleware.AttemptStore
}

func NewShareHandler(svc *service.ShareService, cfg *config.Config, attempt *middleware.AttemptStore) *ShareHandler {
	return &ShareHandler{svc: svc, cfg: cfg, attempt: attempt}
}

// CreateShare POST /api/share  (multipart: files...)
func (h *ShareHandler) CreateShare(c *gin.Context) {
	// 限制单请求体大小（单文件上限 + 1MB 缓冲）
	maxBody := h.cfg.Limits.MaxFileSizeBytes + 1024*1024
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBody)
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "解析表单失败: " + err.Error()})
		return
	}

	form := c.Request.MultipartForm
	if form == nil || len(form.File) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "未提供文件"})
		return
	}

	// 可选的自定义有效期（小时），0 表示用配置默认
	expireHours := 0
	if v := c.PostForm("expireHours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			expireHours = n
		}
	}

	// 收集所有上传的文件句柄
	var files []service.IncomingFile
	for _, fhs := range form.File {
		for _, fh := range fhs {
			if len(files) >= h.cfg.Limits.MaxFilesPerShare {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("文件数超过上限 %d", h.cfg.Limits.MaxFilesPerShare)})
				return
			}
			f, err := fh.Open()
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "打开上传文件失败: " + err.Error()})
				return
			}
			defer f.Close()
			files = append(files, service.IncomingFile{
				Name:   fh.Filename,
				Size:   fh.Size,
				Reader: f,
			})
		}
	}

	sh, err := h.svc.Create(files, expireHours)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, service.ErrInvalidInput):
			status = http.StatusBadRequest
		case errors.Is(err, service.ErrTooManyFiles):
			status = http.StatusBadRequest
		case errors.Is(err, service.ErrFileTooLarge):
			status = http.StatusRequestEntityTooLarge
		case errors.Is(err, service.ErrQuotaExceeded):
			status = http.StatusInsufficientStorage
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":      sh.Code,
		"expiresAt": sh.ExpiresAt,
		"files":     sh.Files,
	})
}

// GetShare GET /api/share/:code  邀请码即访问凭证
func (h *ShareHandler) GetShare(c *gin.Context) {
	code := c.Param("code")
	if !isValidCode(code) {
		h.attempt.RecordFailure(c.ClientIP())
		c.JSON(http.StatusBadRequest, gin.H{"error": "邀请码格式无效"})
		return
	}

	sh, err := h.svc.Get(code)
	if err != nil {
		h.attempt.RecordFailure(c.ClientIP())
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "分享不存在或已过期"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 成功，重置失败计数
	h.attempt.RecordSuccess(c.ClientIP())

	c.JSON(http.StatusOK, gin.H{
		"code":      sh.Code,
		"expiresAt": sh.ExpiresAt,
		"createdAt": sh.CreatedAt,
		"files":     sh.Files,
	})
}

// DownloadFile GET /api/share/:code/files/:fileID  邀请码即凭证
func (h *ShareHandler) DownloadFile(c *gin.Context) {
	code := c.Param("code")
	fileID := c.Param("fileID")

	if !isValidCode(code) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "邀请码格式无效"})
		return
	}

	f, reader, err := h.svc.OpenFile(code, fileID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "分享不存在或已过期"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer reader.Close()

	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeFileName(f.Name)))
	c.Header("Content-Length", fmt.Sprintf("%d", f.Size))
	c.Writer.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(c.Writer, c.Request, f.Name, f.CreatedAt, reader)
}

// isValidCode 6 位数字
func isValidCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// sanitizeFileName 去除控制字符与换行，防止头注入
func sanitizeFileName(name string) string {
	name = strings.ReplaceAll(name, `"`, "'")
	name = strings.ReplaceAll(name, "\r", "")
	name = strings.ReplaceAll(name, "\n", "")
	return name
}
