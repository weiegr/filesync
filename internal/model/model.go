package model

import "time"

// Share 一次分享
type Share struct {
	Code      string    `json:"code"` // 6 位邀请码，主键
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Files     []File    `json:"files"`
}

// File 分享中的单个文件
type File struct {
	ID           string    `json:"id"`           // UUID
	ShareCode    string    `json:"shareCode"`
	Name         string    `json:"name"`         // 原始文件名
	Size         int64     `json:"size"`
	StoredPath   string    `json:"-"`            // 磁盘相对路径，不输出到 JSON
	CreatedAt    time.Time `json:"createdAt"`
}

// Upload 进行中的分片上传会话（尚未生成分享）
type Upload struct {
	ID         string    `json:"id"`         // UUID
	Name       string    `json:"name"`       // 原始文件名
	Size       int64     `json:"size"`
	ChunkSize  int64     `json:"chunkSize"`
	ChunkCount int64     `json:"chunkCount"`
	Received   int64     `json:"received"`   // 已连续接收的字节数
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// ChunkIndex 当前应续传的分片下标（已接收字节数对应的下一个分片）
func (u *Upload) ChunkIndex() int64 {
	if u.ChunkSize <= 0 {
		return 0
	}
	return u.Received / u.ChunkSize
}

// IsExpired 是否已过期
func (s *Share) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

// TotalSize 分享内所有文件总大小
func (s *Share) TotalSize() int64 {
	var total int64
	for _, f := range s.Files {
		total += f.Size
	}
	return total
}
