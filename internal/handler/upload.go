package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"filesync/internal/config"
	"filesync/internal/service"
	"filesync/internal/store"

	"github.com/gin-gonic/gin"
)

// UploadHandler 分片上传 HTTP 处理器
type UploadHandler struct {
	svc *service.UploadService
	cfg *config.Config
}

func NewUploadHandler(svc *service.UploadService, cfg *config.Config) *UploadHandler {
	return &UploadHandler{svc: svc, cfg: cfg}
}

// Init POST /api/upload/init  请求体: {"name":"a.txt","size":1024}
func (h *UploadHandler) Init(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误: " + err.Error()})
		return
	}

	u, err := h.svc.Init(req.Name, req.Size)
	if err != nil {
		h.abort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":         u.ID,
		"name":       u.Name,
		"size":       u.Size,
		"chunkSize":  u.ChunkSize,
		"chunkCount": u.ChunkCount,
		"received":   u.Received,
	})
}

// Chunk POST /api/upload/:id/chunk?index=N   multipart 字段: chunk
func (h *UploadHandler) Chunk(c *gin.Context) {
	id := c.Param("id")
	indexStr := c.Query("index")
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少或无效的分片下标"})
		return
	}

	// 限制单片请求体大小
	maxBody := h.cfg.Limits.ChunkSizeBytes + 1024*1024
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBody)
	if err := c.Request.ParseMultipartForm(8 << 20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "解析分片失败: " + err.Error()})
		return
	}

	form := c.Request.MultipartForm
	if form == nil || len(form.File["chunk"]) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少分片内容"})
		return
	}
	f, err := form.File["chunk"][0].Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "打开分片失败: " + err.Error()})
		return
	}
	defer f.Close()

	u, err := h.svc.WriteChunk(id, index, f)
	if err != nil {
		h.abort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":         u.ID,
		"received":   u.Received,
		"size":       u.Size,
		"chunkCount": u.ChunkCount,
		"complete":   u.Received == u.Size,
	})
}

// Status GET /api/upload/:id/status  查询进度用于断点续传
func (h *UploadHandler) Status(c *gin.Context) {
	u, err := h.svc.Status(c.Param("id"))
	if err != nil {
		h.abort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":         u.ID,
		"name":       u.Name,
		"size":       u.Size,
		"chunkSize":  u.ChunkSize,
		"chunkCount": u.ChunkCount,
		"received":   u.Received,
		"complete":   u.Received == u.Size,
	})
}

// Complete POST /api/upload/complete  请求体: {"uploadIds":[".."],"expireHours":10}
func (h *UploadHandler) Complete(c *gin.Context) {
	var req struct {
		UploadIDs   []string `json:"uploadIds"`
		ExpireHours int      `json:"expireHours"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误: " + err.Error()})
		return
	}

	sh, err := h.svc.Complete(req.UploadIDs, req.ExpireHours)
	if err != nil {
		h.abort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":      sh.Code,
		"expiresAt": sh.ExpiresAt,
		"files":     sh.Files,
	})
}

// abort 将业务错误映射为 HTTP 状态码
func (h *UploadHandler) abort(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, store.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, service.ErrChunkOutOfOrder):
		status = http.StatusConflict
	case errors.Is(err, service.ErrFileTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, service.ErrQuotaExceeded):
		status = http.StatusInsufficientStorage
	case errors.Is(err, service.ErrInvalidInput), errors.Is(err, service.ErrTooManyFiles):
		status = http.StatusBadRequest
	}
	c.JSON(status, gin.H{"error": fmt.Sprintf("%v", err)})
}
