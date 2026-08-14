package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"filesync/internal/model"
)

// UploadRepo 分片上传会话元数据存取
type UploadRepo struct {
	db *sql.DB
}

func NewUploadRepo(db *sql.DB) *UploadRepo {
	return &UploadRepo{db: db}
}

// Create 新建上传会话
func (r *UploadRepo) Create(u *model.Upload) error {
	_, err := r.db.Exec(
		`INSERT INTO uploads (id, name, size, chunk_size, chunk_count, received, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Name, u.Size, u.ChunkSize, u.ChunkCount, u.Received, u.CreatedAt, u.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("插入上传会话失败: %w", err)
	}
	return nil
}

// Get 查询上传会话
func (r *UploadRepo) Get(id string) (*model.Upload, error) {
	u := &model.Upload{}
	err := r.db.QueryRow(
		`SELECT id, name, size, chunk_size, chunk_count, received, created_at, updated_at
		 FROM uploads WHERE id = ?`,
		id,
	).Scan(&u.ID, &u.Name, &u.Size, &u.ChunkSize, &u.ChunkCount, &u.Received, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("查询上传会话失败: %w", err)
	}
	return u, nil
}

// UpdateReceived 更新已接收字节数与更新时间（received 单调递增）
func (r *UploadRepo) UpdateReceived(id string, received int64) error {
	res, err := r.db.Exec(
		`UPDATE uploads SET received = MAX(received, ?), updated_at = ? WHERE id = ?`,
		received, time.Now(), id,
	)
	if err != nil {
		return fmt.Errorf("更新上传进度失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete 删除上传会话
func (r *UploadRepo) Delete(id string) error {
	if _, err := r.db.Exec(`DELETE FROM uploads WHERE id = ?`, id); err != nil {
		return fmt.Errorf("删除上传会话失败: %w", err)
	}
	return nil
}

// ListStale 返回最后更新时间早于 before 的会话 id 列表（用于清理）
func (r *UploadRepo) ListStale(before time.Time) ([]string, error) {
	rows, err := r.db.Query(`SELECT id FROM uploads WHERE updated_at < ?`, before)
	if err != nil {
		return nil, fmt.Errorf("查询过期上传会话失败: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}
