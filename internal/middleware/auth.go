package middleware

import (
	"database/sql"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// AttemptStore 邀请码失败计数 + 锁定（基于 SQLite，单进程足够）
type AttemptStore struct {
	mu   sync.Mutex
	db   *sql.DB
	max  int
	lock time.Duration
}

func NewAttemptStore(db *sql.DB, maxAttempts, lockMinutes int) *AttemptStore {
	return &AttemptStore{db: db, max: maxAttempts, lock: time.Duration(lockMinutes) * time.Minute}
}

// IsLocked 当前 IP 是否处于锁定状态
func (a *AttemptStore) IsLocked(ip string) (bool, time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()

	var lockedUntil sql.NullTime
	_ = a.db.QueryRow(`SELECT locked_until FROM share_code_attempts WHERE ip = ?`, ip).Scan(&lockedUntil)
	if !lockedUntil.Valid {
		return false, time.Time{}
	}
	if time.Now().Before(lockedUntil.Time) {
		return true, lockedUntil.Time
	}
	return false, time.Time{}
}

// RecordFailure 记录一次失败，触发阈值后锁定
func (a *AttemptStore) RecordFailure(ip string) (locked bool, until time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()

	var failures int
	var lockedUntil sql.NullTime
	err := a.db.QueryRow(`SELECT failures, locked_until FROM share_code_attempts WHERE ip = ?`, ip).Scan(&failures, &lockedUntil)
	if err == sql.ErrNoRows {
		failures = 0
	}

	failures++
	if failures >= a.max {
		until = time.Now().Add(a.lock)
		_, err = a.db.Exec(`
			INSERT INTO share_code_attempts (ip, failures, locked_until) VALUES (?, ?, ?)
			ON CONFLICT(ip) DO UPDATE SET failures = excluded.failures, locked_until = excluded.locked_until
		`, ip, failures, until)
		if err == nil {
			return true, until
		}
	} else {
		_, err = a.db.Exec(`
			INSERT INTO share_code_attempts (ip, failures, locked_until) VALUES (?, ?, NULL)
			ON CONFLICT(ip) DO UPDATE SET failures = excluded.failures
		`, ip, failures)
	}
	return false, time.Time{}
}

// RecordSuccess 成功后重置计数
func (a *AttemptStore) RecordSuccess(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, _ = a.db.Exec(`DELETE FROM share_code_attempts WHERE ip = ?`, ip)
}

// LockGuard gin 中间件：对查询请求做锁定预检
func (a *AttemptStore) LockGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if locked, until := a.IsLocked(ip); locked {
			retry := int(time.Until(until).Minutes()) + 1
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":  fmt.Sprintf("尝试过于频繁，已锁定，请约 %d 分钟后再试", retry),
				"locked": true,
			})
			return
		}
		c.Next()
	}
}
