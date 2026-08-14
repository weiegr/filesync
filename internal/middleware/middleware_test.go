package middleware

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	_ "modernc.org/sqlite"
)

func setupAttemptDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + t.TempDir() + "/attempt.db" +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(3000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS share_code_attempts (
		ip TEXT PRIMARY KEY, failures INTEGER NOT NULL DEFAULT 0, locked_until DATETIME
	)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return db
}

func TestAttemptLocking(t *testing.T) {
	db := setupAttemptDB(t)
	as := NewAttemptStore(db, 3, 10) // 3 次失败后锁 10 分钟

	ip := "1.2.3.4"
	// 前 2 次失败不锁定
	for i := 0; i < 2; i++ {
		locked, _ := as.RecordFailure(ip)
		if locked {
			t.Fatalf("第 %d 次失败不应锁定", i+1)
		}
		if lockedNow, _ := as.IsLocked(ip); lockedNow {
			t.Fatalf("第 %d 次失败后不应处于锁定态", i+1)
		}
	}
	// 第 3 次失败锁定
	locked, until := as.RecordFailure(ip)
	if !locked {
		t.Fatal("第 3 次失败应锁定")
	}
	if until.Before(time.Now()) {
		t.Error("锁定时间应在未来")
	}
	if lockedNow, _ := as.IsLocked(ip); !lockedNow {
		t.Error("锁定后 IsLocked 应为 true")
	}

	// 成功后解锁
	as.RecordSuccess(ip)
	if lockedNow, _ := as.IsLocked(ip); lockedNow {
		t.Error("成功后应解锁")
	}
}

func TestLockGuardRejectsWhenLocked(t *testing.T) {
	db := setupAttemptDB(t)
	as := NewAttemptStore(db, 1, 10)
	// 直接锁一个 IP
	as.RecordFailure("9.9.9.9")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(as.LockGuard())
	r.GET("/t", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "9.9.9.9:1234"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("锁定时应 429，得到 %d", rec.Code)
	}
}

func TestRateLimiter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rl := NewRateLimiter(5) // 每分钟 5 次
	r := gin.New()
	r.Use(rl.Handle)
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	// 同一 IP 连续请求，超过 5 次应被限流
	var lastCode int
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "5.5.5.5:1"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		lastCode = rec.Code
	}
	if lastCode != http.StatusTooManyRequests {
		t.Errorf("第 10 次请求应被限流(429)，得到 %d", lastCode)
	}
}
