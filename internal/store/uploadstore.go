package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// UploadStore 分片上传临时文件的磁盘读写
// 数据存放于独立临时目录 data/uploads/<uploadId>/<name>，与正式分享目录隔离
type UploadStore struct {
	baseDir string
}

func NewUploadStore(baseDir string) (*UploadStore, error) {
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return nil, fmt.Errorf("创建上传临时目录失败: %w", err)
	}
	return &UploadStore{baseDir: baseDir}, nil
}

// BaseDir 返回上传临时目录根路径（绝对路径）
func (u *UploadStore) BaseDir() string { return u.baseDir }

// Dir 返回某会话的目录（绝对路径），确保目录存在
func (u *UploadStore) Dir(uploadID string) (string, error) {
	if err := u.validateUploadID(uploadID); err != nil {
		return "", err
	}
	dir := filepath.Join(u.baseDir, uploadID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建上传会话目录失败: %w", err)
	}
	return dir, nil
}

// Create 创建会话临时文件（空文件），返回其绝对路径
func (u *UploadStore) Create(uploadID, name string) (string, error) {
	dir, err := u.Dir(uploadID)
	if err != nil {
		return "", err
	}
	if err := u.validateName(name); err != nil {
		return "", err
	}
	absPath := filepath.Join(dir, name)
	if err := u.ensureWithin(dir, absPath); err != nil {
		return "", err
	}
	f, err := os.OpenFile(absPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			// 已存在（如初始化时被并发调用），复用即可
			return absPath, nil
		}
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	f.Close()
	return absPath, nil
}

// WriteChunk 将 r 内容写入文件偏移 offset 处，返回写入字节数
func (u *UploadStore) WriteChunk(uploadID, name string, offset int64, r io.Reader) (int64, error) {
	dir, err := u.Dir(uploadID)
	if err != nil {
		return 0, err
	}
	if err := u.validateName(name); err != nil {
		return 0, err
	}
	absPath := filepath.Join(dir, name)
	if err := u.ensureWithin(dir, absPath); err != nil {
		return 0, err
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return 0, fmt.Errorf("读取分片失败: %w", err)
	}

	f, err := os.OpenFile(absPath, os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("打开临时文件失败: %w", err)
	}
	defer f.Close()

	n, err := f.WriteAt(data, offset)
	if err != nil {
		return int64(n), fmt.Errorf("写入分片失败: %w", err)
	}
	if err := f.Sync(); err != nil {
		return int64(n), fmt.Errorf("刷盘失败: %w", err)
	}
	return int64(n), nil
}

// RenameTo 将会话临时文件移动到目标绝对路径（用于合并进分享目录）
func (u *UploadStore) RenameTo(uploadID, name, destAbsPath string) error {
	dir, err := u.Dir(uploadID)
	if err != nil {
		return err
	}
	if err := u.validateName(name); err != nil {
		return err
	}
	src := filepath.Join(dir, name)
	if err := u.ensureWithin(dir, src); err != nil {
		return err
	}
	if err := os.Rename(src, destAbsPath); err != nil {
		return fmt.Errorf("移动临时文件失败: %w", err)
	}
	return nil
}

// Delete 删除某会话的整个临时目录
func (u *UploadStore) Delete(uploadID string) error {
	if err := u.validateUploadID(uploadID); err != nil {
		return err
	}
	dir := filepath.Join(u.baseDir, uploadID)
	if err := u.ensureWithin(u.baseDir, dir); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("删除上传临时目录失败: %w", err)
	}
	return nil
}

// validateUploadID 会话 id 必须为合法 UUID，防止目录穿越
func (u *UploadStore) validateUploadID(id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("无效的上传会话 id")
	}
	return nil
}

// validateName 文件名校验：拒绝路径分隔符、..、空字节
func (u *UploadStore) validateName(name string) error {
	if name == "" || len(name) > 255 {
		return fmt.Errorf("文件名为空或过长")
	}
	if contains(name, "/") || contains(name, "\\") {
		return fmt.Errorf("文件名含路径分隔符")
	}
	if contains(name, "..") || contains(name, "\x00") {
		return fmt.Errorf("文件名含非法字符")
	}
	return nil
}

// ensureWithin 确认 absPath 落在 base 下，防止路径穿越
func (u *UploadStore) ensureWithin(base, absPath string) error {
	absClean, err := filepath.Abs(absPath)
	if err != nil {
		return fmt.Errorf("路径解析失败: %w", err)
	}
	baseClean, err := filepath.Abs(base)
	if err != nil {
		return fmt.Errorf("路径解析失败: %w", err)
	}
	rel, err := filepath.Rel(baseClean, absClean)
	if err != nil {
		return fmt.Errorf("路径关系计算失败: %w", err)
	}
	if rel == ".." || (len(rel) >= 2 && rel[:2] == "..") {
		return fmt.Errorf("路径越界")
	}
	return nil
}
