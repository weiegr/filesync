package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// FileStore 磁盘文件读写
type FileStore struct {
	baseDir string
}

func NewFileStore(baseDir string) (*FileStore, error) {
	fs := &FileStore{baseDir: baseDir}
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return nil, fmt.Errorf("创建存储目录失败: %w", err)
	}
	return fs, nil
}

// BaseDir 返回存储根目录（绝对路径）
func (fs *FileStore) BaseDir() string { return fs.baseDir }

// ShareDir 返回某分享的存储目录（绝对路径），确保目录存在
func (fs *FileStore) ShareDir(shareCode string) (string, error) {
	if err := fs.validateCode(shareCode); err != nil {
		return "", err
	}
	dir := filepath.Join(fs.baseDir, shareCode)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建分享目录失败: %w", err)
	}
	return dir, nil
}

// SaveFile 将 reader 内容保存为 shareCode/fileID-name，返回相对路径
func (fs *FileStore) SaveFile(shareCode, fileID, fileName string, r io.Reader) (relPath string, err error) {
	dir, err := fs.ShareDir(shareCode)
	if err != nil {
		return "", err
	}
	if err := fs.validateName(fileName); err != nil {
		return "", err
	}

	// 用 fileID 前缀避免文件名冲突，保留原始名用于下载时显示
	safeName := fmt.Sprintf("%s-%s", fileID, fileName)
	absPath := filepath.Join(dir, safeName)

	// 必须落在 baseDir/shareCode 下
	if err := fs.ensureWithin(dir, absPath); err != nil {
		return "", err
	}

	f, err := os.OpenFile(absPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("创建文件失败: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		_ = os.Remove(absPath) // 失败则清理半成品
		return "", fmt.Errorf("写入文件失败: %w", err)
	}

	rel, err := filepath.Rel(fs.baseDir, absPath)
	if err != nil {
		return "", fmt.Errorf("计算相对路径失败: %w", err)
	}
	return rel, nil
}

// Open 打开磁盘文件用于下载
func (fs *FileStore) Open(relPath string) (*os.File, error) {
	absPath := filepath.Join(fs.baseDir, relPath)
	if err := fs.ensureWithin(fs.baseDir, absPath); err != nil {
		return nil, err
	}
	f, err := os.Open(absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

// DeleteShare 删除某分享的整个目录
func (fs *FileStore) DeleteShare(shareCode string) error {
	if err := fs.validateCode(shareCode); err != nil {
		return err
	}
	dir := filepath.Join(fs.baseDir, shareCode)
	if err := fs.ensureWithin(fs.baseDir, dir); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		// 目录不存在不算错
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("删除分享目录失败: %w", err)
	}
	return nil
}

// TotalUsed 统计当前磁盘占用字节数
func (fs *FileStore) TotalUsed() (int64, error) {
	var total int64
	err := filepath.Walk(fs.baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// 容错：跳过个别错误项
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// validateCode 邀请码必须为 6 位数字，防止目录穿越
func (fs *FileStore) validateCode(code string) error {
	if len(code) != 6 {
		return fmt.Errorf("无效的邀请码长度")
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return fmt.Errorf("邀请码必须为数字")
		}
	}
	return nil
}

// validateName 文件名校验：拒绝路径分隔符、..、空字节
func (fs *FileStore) validateName(name string) error {
	if name == "" || len(name) > 255 {
		return fmt.Errorf("文件名为空或过长")
	}
	if filepath.Separator == '/' && (contains(name, "/") || contains(name, "\\")) {
		return fmt.Errorf("文件名含路径分隔符")
	}
	if contains(name, "..") || contains(name, "\x00") {
		return fmt.Errorf("文件名含非法字符")
	}
	return nil
}

// ensureWithin 确认 absPath 落在 base 下，防止路径穿越
func (fs *FileStore) ensureWithin(base, absPath string) error {
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

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
