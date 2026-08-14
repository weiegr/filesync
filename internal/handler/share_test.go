package handler_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"filesync/internal/config"
	"filesync/internal/handler"
	"filesync/internal/middleware"
	"filesync/internal/service"
	"filesync/internal/store"

	"github.com/gin-gonic/gin"
)

// 组装一个测试用的 router
func newTestRouter(t *testing.T) (*gin.Engine, string, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	cfg := config.Default()
	cfg.Storage.Dir = filepath.Join(dir, "files")
	cfg.Log.Dir = filepath.Join(dir, "logs")
	cfg.Storage.MaxTotalSize = "100MB" // 便于测试配额
	cfg.Limits.MaxFileSize = "1MB"
	// 重新解析大小字节数
	if err := cfg.Validate(); err != nil {
		t.Fatalf("配置校验失败: %v", err)
	}

	dbPath := filepath.Join(dir, "test.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}

	shareStore := store.NewShareStore(db)
	fileStore, err := store.NewFileStore(cfg.Storage.Dir)
	if err != nil {
		t.Fatalf("NewFileStore 失败: %v", err)
	}
	codeGen := service.NewCodeGenerator(shareStore)
	svc := service.NewShareService(shareStore, fileStore, codeGen, cfg)
	attempt := middleware.NewAttemptStore(db, cfg.Security.ShareCodeMaxAttempts, cfg.Security.ShareCodeLockMinutes)
	// 测试中把锁定阈值调大，避免干扰正常流程测试
	attempt = middleware.NewAttemptStore(db, 100, 10)

	h := handler.NewShareHandler(svc, cfg, attempt)

	r := gin.New()
	r.POST("/api/share", h.CreateShare)
	r.GET("/api/share/:code", h.GetShare)
	r.GET("/api/share/:code/files/:fileID", h.DownloadFile)

	return r, dir, func() { db.Close() }
}

// 上传构造 multipart 请求
func multipartRequest(files map[string]string) (*bytes.Buffer, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for name, content := range files {
		fw, err := w.CreateFormFile("files", name)
		if err != nil {
			return nil, "", err
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			return nil, "", err
		}
	}
	w.Close()
	return &buf, w.FormDataContentType(), nil
}

type createResp struct {
	Code      string         `json:"code"`
	ExpiresAt string         `json:"expiresAt"`
	Files     []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Size int64  `json:"size"`
	} `json:"files"`
}

func createShare(t *testing.T, r *gin.Engine, files map[string]string) createResp {
	t.Helper()
	body, ct, err := multipartRequest(files)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/share", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("创建分享失败 HTTP %d: %s", rec.Code, rec.Body.String())
	}
	var resp createResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	return resp
}

func TestFullFlow(t *testing.T) {
	r, _, cleanup := newTestRouter(t)
	defer cleanup()

	resp := createShare(t, r, map[string]string{
		"a.txt":  "content a",
		"b.txt":  "content b",
		"big.bin": "1234567890",
	})
	if len(resp.Code) != 6 {
		t.Fatalf("邀请码应为6位，得到 %q", resp.Code)
	}
	if len(resp.Files) != 3 {
		t.Fatalf("应有3个文件，得到 %d", len(resp.Files))
	}

	// 查询
	req := httptest.NewRequest(http.MethodGet, "/api/share/"+resp.Code, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询失败 HTTP %d", rec.Code)
	}
	var q struct {
		Files []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil {
		t.Fatalf("解析查询失败: %v", err)
	}
	if len(q.Files) != 3 {
		t.Fatalf("查询文件数应为3，得到 %d", len(q.Files))
	}

	// 下载第一个文件，验证内容
	first := q.Files[0]
	req = httptest.NewRequest(http.MethodGet, "/api/share/"+resp.Code+"/files/"+first.ID, nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("下载失败 HTTP %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	// 内容应等于对应文件名内容
	expected := map[string]string{"a.txt": "content a", "b.txt": "content b", "big.bin": "1234567890"}[first.Name]
	if string(body) != expected {
		t.Errorf("下载内容不一致: %q vs %q", body, expected)
	}
}

func TestErrors(t *testing.T) {
	r, _, cleanup := newTestRouter(t)
	defer cleanup()

	// 非法邀请码
	req := httptest.NewRequest(http.MethodGet, "/api/share/abc123", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非法邀请码应 400，得到 %d", rec.Code)
	}

	// 不存在的分享
	req = httptest.NewRequest(http.MethodGet, "/api/share/999999", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("不存在的分享应 404，得到 %d", rec.Code)
	}

	// 无文件上传
	body, ct, _ := multipartRequest(map[string]string{})
	req = httptest.NewRequest(http.MethodPost, "/api/share", body)
	req.Header.Set("Content-Type", ct)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("无文件上传应 400，得到 %d", rec.Code)
	}
}

func TestFileTooLarge(t *testing.T) {
	r, _, cleanup := newTestRouter(t)
	defer cleanup()

	// 超过 1MB 限制的文件
	big := bytes.Repeat([]byte("x"), 1<<20+1) // 1MB + 1
	body, ct, _ := multipartRequest(map[string]string{"big.bin": string(big)})
	req := httptest.NewRequest(http.MethodPost, "/api/share", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("超限文件应 413，得到 %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestTooManyFiles(t *testing.T) {
	r, _, cleanup := newTestRouter(t)
	defer cleanup()

	files := map[string]string{}
	for i := 0; i < 11; i++ {
		files[string(rune('a'+i))+".txt"] = "x"
	}
	body, ct, _ := multipartRequest(files)
	req := httptest.NewRequest(http.MethodPost, "/api/share", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	// 上限 10，11 个应失败（handler 层先拦截或 service 层拦截）
	if rec.Code == http.StatusOK {
		t.Errorf("超过 10 个文件应失败，但成功了")
	}
}

var _ = sql.ErrNoRows
