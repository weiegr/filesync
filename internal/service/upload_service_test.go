package service

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filesync/internal/config"
	"filesync/internal/model"
	"filesync/internal/store"

	"github.com/google/uuid"
)

func newTestUploadService(t *testing.T) (*UploadService, *store.UploadRepo, *store.UploadStore, *store.FileStore, *config.Config) {
	t.Helper()
	dir := t.TempDir()

	cfg := config.Default()
	cfg.Storage.Dir = filepath.Join(dir, "files")
	cfg.Storage.MaxTotalSize = "100MB"
	cfg.Limits.MaxFileSize = "1MB"
	cfg.Limits.ChunkSize = "8" // 8 字节，便于多分片测试
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
	cg := NewCodeGenerator(ss)
	svc := NewUploadService(ur, us, fs, ss, cg, cfg)
	return svc, ur, us, fs, cfg
}

func uploadAll(t *testing.T, svc *UploadService, id string, data []byte) {
	t.Helper()
	u, err := svc.Status(id)
	if err != nil {
		t.Fatalf("Status 失败: %v", err)
	}
	chunkSize := u.ChunkSize
	for i := int64(0); i < u.ChunkCount; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		if _, err := svc.WriteChunk(id, int(i), bytes.NewReader(data[start:end])); err != nil {
			t.Fatalf("写入分片 %d 失败: %v", i, err)
		}
	}
}

func TestUploadInitAndStatus(t *testing.T) {
	svc, _, _, _, _ := newTestUploadService(t)
	u, err := svc.Init("a.bin", 20)
	if err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	if u.ChunkSize != 8 {
		t.Errorf("分片大小应为 8，得到 %d", u.ChunkSize)
	}
	if u.ChunkCount != 3 {
		t.Errorf("20 字节按 8 分片应有 3 片，得到 %d", u.ChunkCount)
	}
	if u.Received != 0 {
		t.Errorf("初始 received 应为 0，得到 %d", u.Received)
	}

	st, err := svc.Status(u.ID)
	if err != nil {
		t.Fatalf("Status 失败: %v", err)
	}
	if st.ID != u.ID {
		t.Errorf("Status 返回的会话不一致")
	}
}

func TestUploadChunksAndComplete(t *testing.T) {
	svc, _, _, fs, _ := newTestUploadService(t)
	data := []byte("0123456789abcdefghij") // 20 字节
	u, err := svc.Init("data.bin", int64(len(data)))
	if err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	uploadAll(t, svc, u.ID, data)

	sh, err := svc.Complete([]string{u.ID}, 0)
	if err != nil {
		t.Fatalf("Complete 失败: %v", err)
	}
	if len(sh.Code) != 6 {
		t.Errorf("邀请码应为 6 位: %q", sh.Code)
	}
	if len(sh.Files) != 1 {
		t.Fatalf("应有 1 个文件，得到 %d", len(sh.Files))
	}
	if sh.Files[0].Size != int64(len(data)) {
		t.Errorf("文件大小不符: %d", sh.Files[0].Size)
	}

	// 校验合并后的文件内容
	f, err := fs.Open(sh.Files[0].StoredPath)
	if err != nil {
		t.Fatalf("打开合并文件失败: %v", err)
	}
	defer f.Close()
	buf := make([]byte, len(data))
	if _, err := f.Read(buf); err != nil {
		t.Fatalf("读取合并文件失败: %v", err)
	}
	if !bytes.Equal(buf, data) {
		t.Errorf("合并内容不一致")
	}

	// 会话应已清理
	if _, err := svc.Status(u.ID); err == nil {
		t.Error("Complete 后会话应已删除")
	}
}

func TestUploadResume(t *testing.T) {
	svc, _, _, _, _ := newTestUploadService(t)
	data := []byte("0123456789abcdefghij") // 20 字节
	u, err := svc.Init("resume.bin", int64(len(data)))
	if err != nil {
		t.Fatalf("Init 失败: %v", err)
	}

	// 只传第一个分片 (8 字节)，模拟中断
	if _, err := svc.WriteChunk(u.ID, 0, bytes.NewReader(data[:8])); err != nil {
		t.Fatalf("写入分片 0 失败: %v", err)
	}
	st, err := svc.Status(u.ID)
	if err != nil {
		t.Fatalf("Status 失败: %v", err)
	}
	if st.Received != 8 {
		t.Errorf("received 应为 8，得到 %d", st.Received)
	}

	// 断点续传：从 ChunkIndex() 开始继续
	restIdx := st.ChunkIndex()
	if restIdx != 1 {
		t.Errorf("续传下标应为 1，得到 %d", restIdx)
	}
	for i := restIdx; i < st.ChunkCount; i++ {
		start := i * u.ChunkSize
		end := start + u.ChunkSize
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		if _, err := svc.WriteChunk(u.ID, int(i), bytes.NewReader(data[start:end])); err != nil {
			t.Fatalf("续传分片 %d 失败: %v", i, err)
		}
	}

	sh, err := svc.Complete([]string{u.ID}, 0)
	if err != nil {
		t.Fatalf("Complete 失败: %v", err)
	}
	if sh.Files[0].Size != int64(len(data)) {
		t.Errorf("续传后文件大小不符")
	}
}

func TestUploadOutOfOrderFails(t *testing.T) {
	svc, _, _, _, _ := newTestUploadService(t)
	u, err := svc.Init("oo.bin", 20)
	if err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	if _, err := svc.WriteChunk(u.ID, 2, bytes.NewReader(make([]byte, 4))); err == nil {
		t.Error("跳过分片 2 应报乱序错误")
	}
}

func TestUploadIncompleteCompleteFails(t *testing.T) {
	svc, _, _, _, _ := newTestUploadService(t)
	u, err := svc.Init("inc.bin", 20)
	if err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	// 只传一片就 complete
	if _, err := svc.WriteChunk(u.ID, 0, bytes.NewReader(make([]byte, 8))); err != nil {
		t.Fatalf("写入分片失败: %v", err)
	}
	if _, err := svc.Complete([]string{u.ID}, 0); err == nil {
		t.Error("未上传完整时 Complete 应报错")
	}
}

func TestUploadCompleteCustomExpiry(t *testing.T) {
	svc, _, _, _, _ := newTestUploadService(t)
	data := []byte("hello world")
	u, err := svc.Init("exp.bin", int64(len(data)))
	if err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	uploadAll(t, svc, u.ID, data)

	sh, err := svc.Complete([]string{u.ID}, 2) // 2 小时有效期
	if err != nil {
		t.Fatalf("Complete 失败: %v", err)
	}
	diff := sh.ExpiresAt.Sub(sh.CreatedAt)
	if diff.Hours() < 1.9 || diff.Hours() > 2.1 {
		t.Errorf("有效期应为 2 小时，得到 %v", diff)
	}
}

func TestUploadCompleteInvalidExpiryFails(t *testing.T) {
	svc, _, _, _, _ := newTestUploadService(t)
	u, err := svc.Init("badexp.bin", 10)
	if err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	uploadAll(t, svc, u.ID, []byte("0123456789"))
	if _, err := svc.Complete([]string{u.ID}, 999); err == nil {
		t.Error("超出范围的有效期应报错")
	}
}

func TestUploadInitTooLargeFails(t *testing.T) {
	svc, _, _, _, _ := newTestUploadService(t)
	if _, err := svc.Init("big.bin", 2*1024*1024); err == nil {
		t.Error("超过单文件上限应报错")
	}
}

func TestUploadCleanupStale(t *testing.T) {
	svc, ur, us, _, _ := newTestUploadService(t)
	old := time.Now().Add(-2 * time.Hour)
	u := &model.Upload{
		ID: uuid.NewString(), Name: "stale.bin", Size: 10, ChunkSize: 8, ChunkCount: 2,
		CreatedAt: old, UpdatedAt: old,
	}
	if err := ur.Create(u); err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	if _, err := us.Create(u.ID, u.Name); err != nil {
		t.Fatalf("创建临时文件失败: %v", err)
	}

	if err := svc.CleanupStale(time.Now()); err != nil {
		t.Fatalf("CleanupStale 失败: %v", err)
	}
	if _, err := ur.Get(u.ID); err == nil {
		t.Error("过期会话应被清理")
	}
	if _, err := os.Stat(filepath.Join(us.BaseDir(), u.ID, u.Name)); !os.IsNotExist(err) {
		t.Errorf("临时文件应被清理，Stat 错误: %v", err)
	}
}
