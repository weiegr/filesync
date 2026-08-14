package service

import (
	"errors"
	"fmt"
	"io"
	"time"

	"filesync/internal/config"
	"filesync/internal/model"
	"filesync/internal/store"

	"github.com/google/uuid"
)

// ErrInvalidInput 输入参数无效
var ErrInvalidInput = errors.New("invalid input")

// ErrQuotaExceeded 磁盘配额超限
var ErrQuotaExceeded = errors.New("quota exceeded")

// ErrTooManyFiles 文件数超限
var ErrTooManyFiles = errors.New("too many files")

// ErrFileTooLarge 单文件超限
var ErrFileTooLarge = errors.New("file too large")

// ShareService 分享业务逻辑
type ShareService struct {
	shareStore *store.ShareStore
	fileStore  *store.FileStore
	codeGen    *CodeGenerator
	cfg        *config.Config
}

func NewShareService(ss *store.ShareStore, fs *store.FileStore, cg *CodeGenerator, cfg *config.Config) *ShareService {
	return &ShareService{shareStore: ss, fileStore: fs, codeGen: cg, cfg: cfg}
}

// IncomingFile 待保存的文件元数据
type IncomingFile struct {
	Name string
	Size int64
	// Reader 在 Save 时被消费；调用方需保证大小与 Size 一致
	Reader io.Reader
}

// Create 创建一次分享：生成邀请码、保存所有文件、写入数据库
// expireHours 为可选自定义有效期（0 表示用配置默认），取值范围 1-168
// 返回完整的 Share（含邀请码）
func (svc *ShareService) Create(files []IncomingFile, expireHours int) (*model.Share, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: 至少上传一个文件", ErrInvalidInput)
	}
	if len(files) > svc.cfg.Limits.MaxFilesPerShare {
		return nil, fmt.Errorf("%w: 文件数 %d 超过上限 %d", ErrTooManyFiles, len(files), svc.cfg.Limits.MaxFilesPerShare)
	}

	expHours, err := svc.resolveExpireHours(expireHours)
	if err != nil {
		return nil, err
	}

	// 大小校验 + 配额检查
	var totalSize int64
	for _, f := range files {
		if f.Size <= 0 {
			return nil, fmt.Errorf("%w: 文件 %s 大小无效", ErrInvalidInput, f.Name)
		}
		if f.Size > svc.cfg.Limits.MaxFileSizeBytes {
			return nil, fmt.Errorf("%w: 文件 %s 超过单文件上限", ErrFileTooLarge, f.Name)
		}
		totalSize += f.Size
	}

	// 先预检磁盘配额
	used, err := svc.fileStore.TotalUsed()
	if err != nil {
		return nil, fmt.Errorf("查询磁盘占用失败: %w", err)
	}
	if used+totalSize > svc.cfg.Storage.MaxTotalSizeBytes {
		return nil, fmt.Errorf("%w: 磁盘配额不足", ErrQuotaExceeded)
	}

	// 生成邀请码
	code, err := svc.codeGen.Generate()
	if err != nil {
		return nil, fmt.Errorf("生成邀请码失败: %w", err)
	}

	now := time.Now()
	sh := &model.Share{
		Code:      code,
		CreatedAt: now,
		ExpiresAt: now.Add(time.Duration(expHours) * time.Hour),
	}

	// 逐个保存文件
	for _, f := range files {
		fileID := uuid.NewString()
		relPath, err := svc.fileStore.SaveFile(code, fileID, f.Name, f.Reader)
		if err != nil {
			// 失败回滚：删除已建的分享目录
			_ = svc.fileStore.DeleteShare(code)
			return nil, fmt.Errorf("保存文件 %s 失败: %w", f.Name, err)
		}
		sh.Files = append(sh.Files, model.File{
			ID:         fileID,
			ShareCode:  code,
			Name:       f.Name,
			Size:       f.Size,
			StoredPath: relPath,
			CreatedAt:  now,
		})
	}

	// 写入数据库
	if err := svc.shareStore.Create(sh); err != nil {
		_ = svc.fileStore.DeleteShare(code)
		return nil, err
	}

	return sh, nil
}

// Get 查询分享（不返回 StoredPath 给调用方，由 handler 控制 JSON 输出）
func (svc *ShareService) Get(code string) (*model.Share, error) {
	sh, err := svc.shareStore.Get(code)
	if err != nil {
		return nil, err
	}
	if sh.IsExpired() {
		// 过期视为不存在
		return nil, store.ErrNotFound
	}
	return sh, nil
}

// OpenFile 打开某分享中的某文件用于下载
func (svc *ShareService) OpenFile(code, fileID string) (*model.File, io.ReadSeekCloser, error) {
	sh, err := svc.shareStore.Get(code)
	if err != nil {
		return nil, nil, err
	}
	if sh.IsExpired() {
		return nil, nil, store.ErrNotFound
	}
	for _, f := range sh.Files {
		if f.ID == fileID {
			r, err := svc.fileStore.Open(f.StoredPath)
			if err != nil {
				return nil, nil, err
			}
			return &f, r, nil
		}
	}
	return nil, nil, store.ErrNotFound
}

// resolveExpireHours 解析自定义有效期：0 表示用配置默认，否则须在 1-168 范围内
func (svc *ShareService) resolveExpireHours(expireHours int) (int, error) {
	if expireHours == 0 {
		return svc.cfg.Limits.ExpireHours, nil
	}
	if expireHours < 1 || expireHours > 168 {
		return 0, fmt.Errorf("%w: 有效期必须在 1-168 小时之间", ErrInvalidInput)
	}
	return expireHours, nil
}
