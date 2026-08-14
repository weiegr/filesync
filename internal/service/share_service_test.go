package service

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filesync/internal/config"
	"filesync/internal/model"
	"filesync/internal/store"
)

func newTestService(t *testing.T) (*ShareService, *store.ShareStore, *store.FileStore, *config.Config) {
	t.Helper()
	dir := t.TempDir()

	cfg := config.Default()
	cfg.Storage.Dir = filepath.Join(dir, "files")
	cfg.Storage.MaxTotalSize = "100MB"
	cfg.Limits.MaxFileSize = "1MB"
	cfg.Limits.MaxFilesPerShare = 10
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
	cg := NewCodeGenerator(ss)
	svc := NewShareService(ss, fs, cg, cfg)
	return svc, ss, fs, cfg
}

func TestCreateAndGet(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	sh, err := svc.Create([]IncomingFile{
		{Name: "a.txt", Size: 5, Reader: bytes.NewReader([]byte("hello"))},
	}, 0)
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	if len(sh.Code) != 6 {
		t.Errorf("邀请码应为6位: %q", sh.Code)
	}
	if !sh.ExpiresAt.After(sh.CreatedAt) {
		t.Error("过期时间应在创建时间之后")
	}

	got, err := svc.Get(sh.Code)
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if len(got.Files) != 1 {
		t.Errorf("应有1个文件，得到 %d", len(got.Files))
	}
}

func TestCodeUniqueness(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	seen := map[string]bool{}
	for i := 0; i < 30; i++ {
		sh, err := svc.Create([]IncomingFile{
			{Name: "f.txt", Size: 3, Reader: bytes.NewReader([]byte("abc"))},
		}, 0)
		if err != nil {
			t.Fatalf("Create 失败: %v", err)
		}
		if seen[sh.Code] {
			t.Errorf("邀请码重复: %s", sh.Code)
		}
		seen[sh.Code] = true
	}
}

func TestCreateEmptyFails(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	if _, err := svc.Create(nil, 0); err == nil {
		t.Error("空文件列表应报错")
	}
}

func TestQuotaExceeded(t *testing.T) {
	svc, _, _, cfg := newTestService(t)
	// 把配额设成很小
	cfg.Storage.MaxTotalSizeBytes = 10
	_, err := svc.Create([]IncomingFile{
		{Name: "big.bin", Size: 20, Reader: bytes.NewReader(make([]byte, 20))},
	}, 0)
	if err == nil {
		t.Error("超出配额应报错")
	}
}

func TestCleanupRemovesExpired(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Storage.Dir = filepath.Join(dir, "files")
	cfg.Limits.MaxFileSize = "1MB"
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

	// 构造一个已过期的分享
	expired := &model.Share{
		Code:      "111111",
		CreatedAt: time.Now().Add(-20 * time.Hour),
		ExpiresAt: time.Now().Add(-10 * time.Hour),
		Files: []model.File{
			{ID: "f1", ShareCode: "111111", Name: "old.bin", Size: 5, StoredPath: "111111/f1-old.bin", CreatedAt: time.Now()},
		},
	}
	if err := ss.Create(expired); err != nil {
		t.Fatalf("写入过期分享失败: %v", err)
	}
	// 写一个磁盘文件模拟
	if _, err := fs.SaveFile("111111", "f1", "old.bin", bytes.NewReader([]byte("data"))); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}

	// 清理
	cleanup := NewCleanup(ss, fs, log.New(os.Stderr, "", 0))
	cleanup.runOnce()

	// 过期分享应从 DB 和磁盘删除
	if _, err := ss.Get("111111"); err == nil {
		t.Error("过期分享应被删除")
	}
	dir2 := filepath.Join(cfg.Storage.Dir, "111111")
	if _, err := os.Stat(dir2); !os.IsNotExist(err) {
		t.Error("过期分享磁盘目录应被删除")
	}
}

func TestExpiredShareReturnsNotFound(t *testing.T) {
	svc, ss, fs, _ := newTestService(t)
	// 手工构造一个已过期分享并落库
	expired := &model.Share{
		Code:      "222222",
		CreatedAt: time.Now().Add(-20 * time.Hour),
		ExpiresAt: time.Now().Add(-10 * time.Hour),
	}
	if err := ss.Create(expired); err != nil {
		t.Fatalf("写入分享失败: %v", err)
	}
	_ = fs
	if _, err := svc.Get("222222"); err != store.ErrNotFound {
		t.Errorf("过期分享 Get 应返回 ErrNotFound，得到 %v", err)
	}
}
