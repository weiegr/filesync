package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Open 打开/创建 SQLite 数据库并执行迁移
func Open(dbPath string) (*sql.DB, error) {
	// _pragma 参数: 外键开启、忙等待 5s、WAL 模式提升并发
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// SQLite 单写多读，连接池设置小一些避免写锁竞争
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS shares (
		code           TEXT PRIMARY KEY,
		created_at     DATETIME NOT NULL,
		expires_at     DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS files (
		id            TEXT PRIMARY KEY,
		share_code    TEXT NOT NULL,
		name          TEXT NOT NULL,
		size          INTEGER NOT NULL,
		stored_path   TEXT NOT NULL,
		created_at    DATETIME NOT NULL,
		FOREIGN KEY (share_code) REFERENCES shares(code) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_files_share_code ON files(share_code);

	CREATE TABLE IF NOT EXISTS uploads (
		id           TEXT PRIMARY KEY,
		name         TEXT NOT NULL,
		size         INTEGER NOT NULL,
		chunk_size   INTEGER NOT NULL,
		chunk_count  INTEGER NOT NULL,
		received     INTEGER NOT NULL DEFAULT 0,
		created_at   DATETIME NOT NULL,
		updated_at   DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS share_code_attempts (
		ip            TEXT PRIMARY KEY,
		failures      INTEGER NOT NULL DEFAULT 0,
		locked_until  DATETIME
	);
	`
	_, err := db.Exec(schema)
	if err != nil {
		return fmt.Errorf("数据库迁移失败: %w", err)
	}
	return nil
}
