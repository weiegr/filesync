package handler_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"filesync/internal/config"
	"filesync/internal/handler"
	"filesync/internal/middleware"
	"filesync/internal/service"
	"filesync/internal/store"

	"github.com/gin-gonic/gin"
)

func newUploadRouter(t *testing.T) (*gin.Engine, *service.UploadService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()

	cfg := config.Default()
	cfg.Storage.Dir = filepath.Join(dir, "files")
	cfg.Storage.MaxTotalSize = "100MB"
	cfg.Limits.MaxFileSize = "1MB"
	cfg.Limits.ChunkSize = "8"
	cfg.Limits.ExpireHours = 10
	if err := cfg.Validate(); err != nil {
		t.Fatalf("配置校验失败: %v", err)
	}

	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ss := store.NewShareStore(db)
	fs, err := store.NewFileStore(cfg.Storage.Dir)
	if err != nil {
		t.Fatalf("NewFileStore 失败: %v", err)
	}
	us, err := store.NewUploadStore(filepath.Join(dir, "uploads"))
	if err != nil {
		t.Fatalf("NewUploadStore 失败: %v", err)
	}
	ur := store.NewUploadRepo(db)
	cg := service.NewCodeGenerator(ss)
	svc := service.NewUploadService(ur, us, fs, ss, cg, cfg)
	attempt := middleware.NewAttemptStore(db, 100, 10)

	uploadH := handler.NewUploadHandler(svc, cfg)
	_ = handler.NewShareHandler(service.NewShareService(ss, fs, cg, cfg), cfg, attempt)

	r := gin.New()
	api := r.Group("/api")
	{
		api.POST("/upload/init", uploadH.Init)
		api.POST("/upload/:id/chunk", uploadH.Chunk)
		api.GET("/upload/:id/status", uploadH.Status)
		api.POST("/upload/complete", uploadH.Complete)
	}
	return r, svc
}

func doJSON(t *testing.T, r *gin.Engine, method, url string, body interface{}) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("编码失败: %v", err)
		}
	}
	req := httptest.NewRequest(method, url, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec, resp
}

func chunkRequest(t *testing.T, id string, index int, data []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("chunk", "blob")
	if err != nil {
		t.Fatalf("创建分片表单失败: %v", err)
	}
	fw.Write(data)
	mw.Close()
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/upload/%s/chunk?index=%d", id, index), &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestUploadFullFlowHTTP(t *testing.T) {
	r, _ := newUploadRouter(t)
	data := []byte("0123456789abcdefghij") // 20 字节 -> 3 片 (8,8,4)

	rec, resp := doJSON(t, r, "POST", "/api/upload/init", map[string]interface{}{"name": "a.bin", "size": 20})
	if rec.Code != 200 {
		t.Fatalf("init 状态码 %d: %s", rec.Code, rec.Body.String())
	}
	id := resp["id"].(string)

	// 传 3 片
	for i := 0; i < 3; i++ {
		start := i * 8
		end := start + 8
		if end > len(data) {
			end = len(data)
		}
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, chunkRequest(t, id, i, data[start:end]))
		if rec.Code != 200 {
			t.Fatalf("分片 %d 状态码 %d: %s", i, rec.Code, rec.Body.String())
		}
	}

	// status
	rec, resp = doJSON(t, r, "GET", "/api/upload/"+id+"/status", nil)
	if rec.Code != 200 {
		t.Fatalf("status 状态码 %d", rec.Code)
	}
	if int(resp["received"].(float64)) != 20 {
		t.Errorf("received 应为 20，得到 %v", resp["received"])
	}

	// complete
	rec, resp = doJSON(t, r, "POST", "/api/upload/complete", map[string]interface{}{"uploadIds": []string{id}})
	if rec.Code != 200 {
		t.Fatalf("complete 状态码 %d: %s", rec.Code, rec.Body.String())
	}
	code, _ := resp["code"].(string)
	if len(code) != 6 {
		t.Errorf("邀请码应为 6 位: %q", code)
	}
}

func TestUploadResumeHTTP(t *testing.T) {
	r, _ := newUploadRouter(t)
	data := []byte("0123456789abcdefghij")
	_, resp := doJSON(t, r, "POST", "/api/upload/init", map[string]interface{}{"name": "r.bin", "size": 20})
	id := resp["id"].(string)

	// 只传第 0 片，模拟中断
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, chunkRequest(t, id, 0, data[:8]))
	if rec.Code != 200 {
		t.Fatalf("分片0 状态码 %d", rec.Code)
	}

	// 查询续传位置
	_, st := doJSON(t, r, "GET", "/api/upload/"+id+"/status", nil)
	if int(st["received"].(float64)) != 8 {
		t.Fatalf("received 应为 8，得到 %v", st["received"])
	}
	nextIdx := int(st["received"].(float64)) / int(st["chunkSize"].(float64))

	// 从 nextIdx 续传
	for i := nextIdx; i < 3; i++ {
		start := i * 8
		end := start + 8
		if end > len(data) {
			end = len(data)
		}
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, chunkRequest(t, id, i, data[start:end]))
		if rec.Code != 200 {
			t.Fatalf("续传分片 %d 状态码 %d: %s", i, rec.Code, rec.Body.String())
		}
	}

	rec, _ = doJSON(t, r, "POST", "/api/upload/complete", map[string]interface{}{"uploadIds": []string{id}})
	if rec.Code != 200 {
		t.Fatalf("complete 状态码 %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUploadOutOfOrderHTTP(t *testing.T) {
	r, _ := newUploadRouter(t)
	_, resp := doJSON(t, r, "POST", "/api/upload/init", map[string]interface{}{"name": "oo.bin", "size": 20})
	id := resp["id"].(string)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, chunkRequest(t, id, 2, make([]byte, 4)))
	if rec.Code != 409 {
		t.Errorf("乱序分片应返回 409，得到 %d", rec.Code)
	}
}

func TestUploadCustomExpiryHTTP(t *testing.T) {
	r, _ := newUploadRouter(t)
	data := []byte("hello world")
	_, resp := doJSON(t, r, "POST", "/api/upload/init", map[string]interface{}{"name": "e.bin", "size": 11})
	id := resp["id"].(string)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, chunkRequest(t, id, 0, data[:8]))
	if rec.Code != 200 {
		t.Fatalf("分片0 状态码 %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, chunkRequest(t, id, 1, data[8:]))
	if rec.Code != 200 {
		t.Fatalf("分片1 状态码 %d", rec.Code)
	}

	_, cr := doJSON(t, r, "POST", "/api/upload/complete", map[string]interface{}{"uploadIds": []string{id}, "expireHours": 2})
	expStr, ok := cr["expiresAt"].(string)
	if !ok {
		t.Fatalf("响应缺少 expiresAt: %v", cr)
	}
	expiresAt, _ := time.Parse(time.RFC3339Nano, expStr)
	diff := time.Until(expiresAt)
	if diff.Hours() < 1.9 || diff.Hours() > 2.1 {
		t.Errorf("有效期应为约 2 小时，得到 %v", diff)
	}
}

func TestUploadInvalidExpiryHTTP(t *testing.T) {
	r, _ := newUploadRouter(t)
	_, resp := doJSON(t, r, "POST", "/api/upload/init", map[string]interface{}{"name": "e.bin", "size": 5})
	id := resp["id"].(string)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, chunkRequest(t, id, 0, []byte("hello")))

	rec, _ = doJSON(t, r, "POST", "/api/upload/complete", map[string]interface{}{"uploadIds": []string{id}, "expireHours": 999})
	if rec.Code != 400 {
		t.Errorf("无效有效期应返回 400，得到 %d", rec.Code)
	}
}
