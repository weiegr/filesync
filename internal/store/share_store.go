package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"filesync/internal/model"
)

// ErrNotFound 未找到
var ErrNotFound = errors.New("not found")

// ShareStore 分享元数据存取
type ShareStore struct {
	db *sql.DB
}

func NewShareStore(db *sql.DB) *ShareStore {
	return &ShareStore{db: db}
}

// Create 创建分享（含文件列表，单事务）
func (s *ShareStore) Create(sh *model.Share) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(
		`INSERT INTO shares (code, created_at, expires_at) VALUES (?, ?, ?)`,
		sh.Code, sh.CreatedAt, sh.ExpiresAt,
	); err != nil {
		return fmt.Errorf("插入分享失败: %w", err)
	}

	for _, f := range sh.Files {
		if _, err := tx.Exec(
			`INSERT INTO files (id, share_code, name, size, stored_path, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			f.ID, f.ShareCode, f.Name, f.Size, f.StoredPath, f.CreatedAt,
		); err != nil {
			return fmt.Errorf("插入文件记录失败: %w", err)
		}
	}

	return tx.Commit()
}

// Get 查询单个分享及其文件列表
func (s *ShareStore) Get(code string) (*model.Share, error) {
	sh := &model.Share{}
	err := s.db.QueryRow(
		`SELECT code, created_at, expires_at FROM shares WHERE code = ?`,
		code,
	).Scan(&sh.Code, &sh.CreatedAt, &sh.ExpiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("查询分享失败: %w", err)
	}

	rows, err := s.db.Query(
		`SELECT id, share_code, name, size, stored_path, created_at FROM files WHERE share_code = ? ORDER BY created_at`,
		code,
	)
	if err != nil {
		return nil, fmt.Errorf("查询文件列表失败: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var f model.File
		if err := rows.Scan(&f.ID, &f.ShareCode, &f.Name, &f.Size, &f.StoredPath, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("扫描文件记录失败: %w", err)
		}
		sh.Files = append(sh.Files, f)
	}

	return sh, nil
}

// Delete 删除分享（CASCADE 会自动删 files 表对应行）
func (s *ShareStore) Delete(code string) error {
	_, err := s.db.Exec(`DELETE FROM shares WHERE code = ?`, code)
	if err != nil {
		return fmt.Errorf("删除分享失败: %w", err)
	}
	return nil
}

// ListExpired 返回已过期的分享 code 列表
func (s *ShareStore) ListExpired(now time.Time) ([]string, error) {
	rows, err := s.db.Query(`SELECT code FROM shares WHERE expires_at < ?`, now)
	if err != nil {
		return nil, fmt.Errorf("查询过期分享失败: %w", err)
	}
	defer rows.Close()

	var codes []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		codes = append(codes, c)
	}
	return codes, nil
}

// CodeExists 检查邀请码是否已存在
func (s *ShareStore) CodeExists(code string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(`SELECT 1 FROM shares WHERE code = ?`, code).Scan(&exists)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
