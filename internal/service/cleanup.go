package service

import (
	"fmt"
	"log"
	"time"

	"filesync/internal/store"
)

// Cleanup 过期清理 goroutine
type Cleanup struct {
	shareStore *store.ShareStore
	fileStore  *store.FileStore
	uploadSvc  *UploadService
	uploadTTL  time.Duration
	logger     *log.Logger
	interval   time.Duration
}

func NewCleanup(ss *store.ShareStore, fs *store.FileStore, logger *log.Logger) *Cleanup {
	return &Cleanup{
		shareStore: ss,
		fileStore:  fs,
		logger:     logger,
		interval:   60 * time.Second,
	}
}

// SetUploadCleanup 启用未完成上传会话的定期清理
func (c *Cleanup) SetUploadCleanup(uploadSvc *UploadService, ttlHours int) {
	c.uploadSvc = uploadSvc
	c.uploadTTL = time.Duration(ttlHours) * time.Hour
}

// Start 启动后台清理，返回停止函数
func (c *Cleanup) Start() (stop func()) {
	ticker := time.NewTicker(c.interval)
	done := make(chan struct{})

	go func() {
		c.runOnce() // 启动时先跑一次
		for {
			select {
			case <-ticker.C:
				c.runOnce()
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()

	return func() { close(done) }
}

func (c *Cleanup) runOnce() {
	// 清理过期分享
	codes, err := c.shareStore.ListExpired(time.Now())
	if err != nil {
		c.logger.Printf("[cleanup] 查询过期分享失败: %v", err)
		return
	}
	for _, code := range codes {
		// 先删磁盘，再删 DB；磁盘删失败不删 DB，等下次重试
		if err := c.fileStore.DeleteShare(code); err != nil {
			c.logger.Printf("[cleanup] 删除分享 %s 磁盘文件失败: %v", code, err)
			continue
		}
		if err := c.shareStore.Delete(code); err != nil {
			c.logger.Printf("[cleanup] 删除分享 %s DB 记录失败: %v", code, err)
			continue
		}
		c.logger.Printf("[cleanup] 已清理过期分享 %s", code)
	}

	// 清理超时未完成的上传会话
	if c.uploadSvc != nil {
		if err := c.uploadSvc.CleanupStale(time.Now().Add(-c.uploadTTL)); err != nil {
			c.logger.Printf("[cleanup] 清理上传会话失败: %v", err)
		}
	}
}

// Interval 用于测试时调整
func (c *Cleanup) SetInterval(d time.Duration) { c.interval = d }

func (c *Cleanup) String() string { return fmt.Sprintf("Cleanup(interval=%s)", c.interval) }
