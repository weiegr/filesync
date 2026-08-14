package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// RateLimiter 基于 IP 的令牌桶限流
type RateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rate     int           // 每分钟允许请求数
	interval time.Duration // 桶刷新间隔
}

type bucket struct {
	tokens   int
	lastFill time.Time
}

func NewRateLimiter(perMin int) *RateLimiter {
	rl := &RateLimiter{
		buckets:  make(map[string]*bucket),
		rate:     perMin,
		interval: time.Minute,
	}
	// 后台清理长时间不用的桶，避免内存泄漏
	go rl.gc()
	return rl
}

func (rl *RateLimiter) gc() {
	t := time.NewTicker(5 * time.Minute)
	for range t.C {
		rl.mu.Lock()
		cutoff := time.Now().Add(-10 * time.Minute)
		for ip, b := range rl.buckets {
			if b.lastFill.Before(cutoff) {
				delete(rl.buckets, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// Handle 限流中间件
func (rl *RateLimiter) Handle(c *gin.Context) {
	ip := c.ClientIP()

	rl.mu.Lock()
	b, ok := rl.buckets[ip]
	if !ok {
		b = &bucket{tokens: rl.rate, lastFill: time.Now()}
		rl.buckets[ip] = b
	}
	// 按时间差补充令牌
	elapsed := time.Since(b.lastFill)
	refill := int(elapsed.Seconds() * float64(rl.rate) / 60.0)
	if refill > 0 {
		b.tokens += refill
		if b.tokens > rl.rate {
			b.tokens = rl.rate
		}
		b.lastFill = time.Now()
	}
	if b.tokens <= 0 {
		rl.mu.Unlock()
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error": "请求过于频繁，请稍后再试",
		})
		return
	}
	b.tokens--
	rl.mu.Unlock()

	c.Next()
}
