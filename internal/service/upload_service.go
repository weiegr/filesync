package service

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"filesync/internal/config"
	"filesync/internal/model"
	"filesync/internal/store"

	"github.com/google/uuid"
)

// ErrChunkOutOfOrder 分片乱序（缺少前序分片）
var ErrChunkOutOfOrder = errors.New("chunk out of order")

// UploadService 分片上传业务逻辑
type UploadService struct {
	uploadRepo  *store.UploadRepo
	uploadStore *store.UploadStore
	fileStore   *store.FileStore
	shareStore  *store.ShareStore
	codeGen     *CodeGenerator
	cfg         *config.Config
}

func NewUploadService(repo *store.UploadRepo, us *store.UploadStore, fs *store.FileStore,
	ss *store.ShareStore, cg *CodeGenerator, cfg *config.Config) *UploadService {
	return &UploadService{uploadRepo: repo, uploadStore: us, fileStore: fs, shareStore: ss, codeGen: cg, cfg: cfg}
}

// Init 初始化一个分片上传会话（校验文件名、大小、配额）
func (s *UploadService) Init(name string, size int64) (*model.Upload, error) {
	if err := s.validateName(name); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if size <= 0 {
		return nil, fmt.Errorf("%w: 文件大小无效", ErrInvalidInput)
	}
	if size > s.cfg.Limits.MaxFileSizeBytes {
		return nil, fmt.Errorf("%w: 文件超过单文件上限", ErrFileTooLarge)
	}

	// 配额预检
	used, err := s.fileStore.TotalUsed()
	if err != nil {
		return nil, fmt.Errorf("查询磁盘占用失败: %w", err)
	}
	if used+size > s.cfg.Storage.MaxTotalSizeBytes {
		return nil, fmt.Errorf("%w: 磁盘配额不足", ErrQuotaExceeded)
	}

	id := uuid.NewString()
	chunkSize := s.cfg.Limits.ChunkSizeBytes
	chunkCount := (size + chunkSize - 1) / chunkSize
	if chunkCount < 1 {
		chunkCount = 1
	}
	now := time.Now()
	u := &model.Upload{
		ID:         id,
		Name:       name,
		Size:       size,
		ChunkSize:  chunkSize,
		ChunkCount: chunkCount,
		Received:   0,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if _, err := s.uploadStore.Create(id, name); err != nil {
		return nil, err
	}
	if err := s.uploadRepo.Create(u); err != nil {
		_ = s.uploadStore.Delete(id)
		return nil, err
	}
	return u, nil
}

// WriteChunk 写入一个分片。分片按顺序写入（offset==received），允许重传已接收分片
func (s *UploadService) WriteChunk(id string, index int, r io.Reader) (*model.Upload, error) {
	u, err := s.uploadRepo.Get(id)
	if err != nil {
		return nil, err
	}
	if index < 0 || int64(index) >= u.ChunkCount {
		return nil, fmt.Errorf("%w: 分片下标越界", ErrInvalidInput)
	}

	offset := int64(index) * u.ChunkSize
	if offset > u.Received {
		return nil, fmt.Errorf("%w: 缺少前序分片（已接收 %d 字节）", ErrChunkOutOfOrder, u.Received)
	}

	// 该分片期望长度（末片可能不足一个分片大小）
	expected := u.ChunkSize
	if offset+u.ChunkSize > u.Size {
		expected = u.Size - offset
	}

	n, err := s.uploadStore.WriteChunk(id, u.Name, offset, r)
	if err != nil {
		return nil, err
	}
	if n != expected {
		return nil, fmt.Errorf("%w: 分片长度 %d 与预期 %d 不符", ErrInvalidInput, n, expected)
	}

	// received 单调递增（重传时保持不变）
	if end := offset + n; end > u.Received {
		if err := s.uploadRepo.UpdateReceived(id, end); err != nil {
			return nil, err
		}
	}
	return s.uploadRepo.Get(id)
}

// Status 查询上传进度（用于断点续传）
func (s *UploadService) Status(id string) (*model.Upload, error) {
	return s.uploadRepo.Get(id)
}

// Complete 合并多个上传会话为一个分享：校验完整性、生成邀请码、移动文件、写库
func (s *UploadService) Complete(ids []string, expireHours int) (*model.Share, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: 未提供上传会话", ErrInvalidInput)
	}
	if len(ids) > s.cfg.Limits.MaxFilesPerShare {
		return nil, fmt.Errorf("%w: 文件数超过上限 %d", ErrTooManyFiles, s.cfg.Limits.MaxFilesPerShare)
	}
	expHours, err := s.resolveExpireHours(expireHours)
	if err != nil {
		return nil, err
	}

	// 拉取全部会话并校验完整性
	var uploads []*model.Upload
	var totalSize int64
	for _, id := range ids {
		u, err := s.uploadRepo.Get(id)
		if err != nil {
			return nil, err
		}
		if u.Received != u.Size {
			return nil, fmt.Errorf("%w: 文件 %s 未上传完整（%d/%d 字节）", ErrInvalidInput, u.Name, u.Received, u.Size)
		}
		uploads = append(uploads, u)
		totalSize += u.Size
	}

	// 配额复检
	used, err := s.fileStore.TotalUsed()
	if err != nil {
		return nil, fmt.Errorf("查询磁盘占用失败: %w", err)
	}
	if used+totalSize > s.cfg.Storage.MaxTotalSizeBytes {
		return nil, fmt.Errorf("%w: 磁盘配额不足", ErrQuotaExceeded)
	}

	code, err := s.codeGen.Generate()
	if err != nil {
		return nil, fmt.Errorf("生成邀请码失败: %w", err)
	}

	now := time.Now()
	shareDir, err := s.fileStore.ShareDir(code)
	if err != nil {
		return nil, err
	}

	sh := &model.Share{
		Code:      code,
		CreatedAt: now,
		ExpiresAt: now.Add(time.Duration(expHours) * time.Hour),
	}

	// 逐个把临时文件移入分享目录
	for _, u := range uploads {
		fileID := uuid.NewString()
		safeName := fmt.Sprintf("%s-%s", fileID, u.Name)
		destAbs := filepath.Join(shareDir, safeName)
		if err := s.uploadStore.RenameTo(u.ID, u.Name, destAbs); err != nil {
			_ = s.fileStore.DeleteShare(code)
			return nil, fmt.Errorf("合并文件 %s 失败: %w", u.Name, err)
		}
		rel, err := filepath.Rel(s.fileStore.BaseDir(), destAbs)
		if err != nil {
			_ = s.fileStore.DeleteShare(code)
			return nil, err
		}
		sh.Files = append(sh.Files, model.File{
			ID:         fileID,
			ShareCode:  code,
			Name:       u.Name,
			Size:       u.Size,
			StoredPath: rel,
			CreatedAt:  now,
		})
	}

	// 写库（成功后才清理上传会话）
	if err := s.shareStore.Create(sh); err != nil {
		_ = s.fileStore.DeleteShare(code)
		return nil, err
	}
	for _, u := range uploads {
		_ = s.uploadRepo.Delete(u.ID)
		_ = s.uploadStore.Delete(u.ID)
	}
	return sh, nil
}

// CleanupStale 清理最后更新早于 before 的未完成上传会话及其临时文件
func (s *UploadService) CleanupStale(before time.Time) error {
	ids, err := s.uploadRepo.ListStale(before)
	if err != nil {
		return err
	}
	for _, id := range ids {
		_ = s.uploadStore.Delete(id)
		_ = s.uploadRepo.Delete(id)
	}
	return nil
}

// resolveExpireHours 0 表示用配置默认，否则须在 1-168 范围内
func (s *UploadService) resolveExpireHours(expireHours int) (int, error) {
	if expireHours == 0 {
		return s.cfg.Limits.ExpireHours, nil
	}
	if expireHours < 1 || expireHours > 168 {
		return 0, fmt.Errorf("%w: 有效期必须在 1-168 小时之间", ErrInvalidInput)
	}
	return expireHours, nil
}

// validateName 文件名校验（与上传存储层一致，用于返回明确的输入错误）
func (s *UploadService) validateName(name string) error {
	if name == "" || len(name) > 255 {
		return errors.New("文件名为空或过长")
	}
	if strings.ContainsAny(name, "/\\") {
		return errors.New("文件名含路径分隔符")
	}
	if strings.Contains(name, "..") || strings.Contains(name, "\x00") {
		return errors.New("文件名含非法字符")
	}
	return nil
}
