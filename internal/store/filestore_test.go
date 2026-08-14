package store

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestFileStore(t *testing.T) *FileStore {
	t.Helper()
	dir := t.TempDir()
	fs, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore 失败: %v", err)
	}
	return fs
}

func TestSaveAndOpen(t *testing.T) {
	fs := newTestFileStore(t)
	content := []byte("hello world")
	rel, err := fs.SaveFile("123456", "uuid-1", "test.txt", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("SaveFile 失败: %v", err)
	}
	// 相对路径应包含邀请码目录
	if !strings.Contains(rel, "123456") {
		t.Errorf("相对路径应为 %q 包含 123456", rel)
	}

	f, err := fs.Open(rel)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer f.Close()
	buf := make([]byte, len(content))
	if _, err := f.Read(buf); err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	if !bytes.Equal(buf, content) {
		t.Errorf("内容不一致: %q vs %q", buf, content)
	}
}

func TestRejectTraversal(t *testing.T) {
	fs := newTestFileStore(t)

	// 非法文件名（路径穿越尝试）
	badNames := []string{
		"../evil.txt",
		"..\\evil.txt",
		"a/../../evil.txt",
		"/etc/passwd",
		"..",
		"x\x00y.txt",
	}
	for _, name := range badNames {
		if _, err := fs.SaveFile("123456", "u1", name, bytes.NewReader([]byte("x"))); err == nil {
			t.Errorf("期望拒绝恶意文件名 %q，但成功了", name)
		}
	}

	// 非法邀请码
	for _, code := range []string{"abc123", "12345", "1234567", "12345x"} {
		if _, err := fs.ShareDir(code); err == nil {
			t.Errorf("期望拒绝非法邀请码 %q，但成功了", code)
		}
	}

	// Open 路径穿越
	for _, p := range []string{"../../etc/passwd", "..\\..\\boot.ini", "/etc/hosts"} {
		if _, err := fs.Open(p); err == nil {
			t.Errorf("期望拒绝路径穿越 %q，但成功了", p)
		}
	}
}

func TestTotalUsed(t *testing.T) {
	fs := newTestFileStore(t)
	if _, err := fs.SaveFile("123456", "u1", "a.bin", bytes.NewReader(make([]byte, 100))); err != nil {
		t.Fatalf("SaveFile 失败: %v", err)
	}
	used, err := fs.TotalUsed()
	if err != nil {
		t.Fatalf("TotalUsed 失败: %v", err)
	}
	if used != 100 {
		t.Errorf("TotalUsed = %d, 期望 100", used)
	}
}

func TestDeleteShare(t *testing.T) {
	fs := newTestFileStore(t)
	if _, err := fs.SaveFile("123456", "u1", "a.bin", bytes.NewReader([]byte("data"))); err != nil {
		t.Fatalf("SaveFile 失败: %v", err)
	}
	shareDir := filepath.Join(fs.baseDir, "123456")
	if _, err := os.Stat(shareDir); err != nil {
		t.Fatalf("分享目录应存在: %v", err)
	}
	if err := fs.DeleteShare("123456"); err != nil {
		t.Fatalf("DeleteShare 失败: %v", err)
	}
	if _, err := os.Stat(shareDir); !os.IsNotExist(err) {
		t.Errorf("删除后分享目录应不存在")
	}
}
