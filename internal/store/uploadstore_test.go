package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func newTestUploadStore(t *testing.T) *UploadStore {
	t.Helper()
	us, err := NewUploadStore(filepath.Join(t.TempDir(), "uploads"))
	if err != nil {
		t.Fatalf("NewUploadStore 失败: %v", err)
	}
	return us
}

func TestUploadWriteChunkAndReadBack(t *testing.T) {
	us := newTestUploadStore(t)
	id := uuid.NewString()
	name := "data.bin"
	path, err := us.Create(id, name)
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	// 写入两个分片到不同偏移
	if _, err := us.WriteChunk(id, name, 0, bytes.NewReader([]byte("abcdefgh"))); err != nil {
		t.Fatalf("写分片1失败: %v", err)
	}
	if _, err := us.WriteChunk(id, name, 8, bytes.NewReader([]byte("ijkl"))); err != nil {
		t.Fatalf("写分片2失败: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if string(data) != "abcdefghijkl" {
		t.Errorf("内容不符: %q", data)
	}
}

func TestUploadRejectInvalidID(t *testing.T) {
	us := newTestUploadStore(t)
	// 目录穿越
	if _, err := us.Create("../evil", "x.txt"); err == nil {
		t.Error("非法 uploadId 应报错")
	}
	if err := us.Delete("../../etc"); err == nil {
		t.Error("非法 uploadId Delete 应报错")
	}
	// 非法文件名
	id := uuid.NewString()
	if _, err := us.Create(id, "../escape.txt"); err == nil {
		t.Error("非法文件名应报错")
	}
}

func TestUploadDelete(t *testing.T) {
	us := newTestUploadStore(t)
	id := uuid.NewString()
	if _, err := us.Create(id, "a.bin"); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	if err := us.Delete(id); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(us.BaseDir(), id)); !os.IsNotExist(err) {
		t.Error("删除后目录应不存在")
	}
}

func TestUploadRenameTo(t *testing.T) {
	us := newTestUploadStore(t)
	id := uuid.NewString()
	name := "a.bin"
	src, err := us.Create(id, name)
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	if err := os.WriteFile(src, []byte("hello"), 0o600); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "dest.bin")
	if err := us.RenameTo(id, name, dest); err != nil {
		t.Fatalf("RenameTo 失败: %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("读取目标失败: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("移动后内容不符")
	}
}
