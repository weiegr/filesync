package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 全局配置
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Storage  StorageConfig  `yaml:"storage"`
	Limits   LimitsConfig    `yaml:"limits"`
	Security SecurityConfig `yaml:"security"`
	Log      LogConfig       `yaml:"log"`
}

type ServerConfig struct {
	Port            int           `yaml:"port"`
	ShutdownTimeout time.Duration `yaml:"shutdownTimeout"`
	// TrustProxy 当部署在 nginx 后时为 true，从 X-Forwarded-* 读取真实 IP
	TrustProxy bool `yaml:"trustProxy"`
}

type StorageConfig struct {
	Dir          string `yaml:"dir"`
	MaxTotalSize string `yaml:"maxTotalSize"` // 字符串形式如 50GB，运行时解析为字节
	// MaxTotalSizeBytes 由 MaxTotalSize 解析得来，供代码使用
	MaxTotalSizeBytes int64 `yaml:"-"`
}

type LimitsConfig struct {
	MaxFileSize       string `yaml:"maxFileSize"`
	MaxFileSizeBytes  int64  `yaml:"-"`
	MaxFilesPerShare  int    `yaml:"maxFilesPerShare"`
	ExpireHours       int    `yaml:"expireHours"`
	ChunkSize         string `yaml:"chunkSize"`         // 分片上传每片大小
	ChunkSizeBytes    int64  `yaml:"-"`
	UploadTTLHours    int    `yaml:"uploadTTLHours"`    // 未完成上传会话保留时长(小时)
}

type SecurityConfig struct {
	// RateLimit 每 IP 每分钟允许的请求数
	RateLimitPerMin int `yaml:"rateLimitPerMin"`
	// ShareCodeMaxAttempts 邀请码连续失败最大次数
	ShareCodeMaxAttempts int `yaml:"shareCodeMaxAttempts"`
	// ShareCodeLockMinutes 超过失败次数后的锁定时长（分钟）
	ShareCodeLockMinutes int `yaml:"shareCodeLockMinutes"`
}

type LogConfig struct {
	Dir string `yaml:"dir"`
}

// Default 返回带合理默认值的配置
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Port:            8080,
			ShutdownTimeout: 15 * time.Second,
			TrustProxy:      true,
		},
		Storage: StorageConfig{
			Dir:          "./data/files",
			MaxTotalSize: "50GB",
		},
		Limits: LimitsConfig{
			MaxFileSize:      "500MB",
			MaxFilesPerShare: 10,
			ExpireHours:      10,
			ChunkSize:        "8MB",
			UploadTTLHours:   24,
		},
		Security: SecurityConfig{
			RateLimitPerMin:     120,
			ShareCodeMaxAttempts: 5,
			ShareCodeLockMinutes: 10,
		},
		Log: LogConfig{
			Dir: "./data/logs",
		},
	}
}

// Load 从 YAML 文件加载配置，文件不存在则返回默认值
func Load(path string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// 文件不存在，使用默认值但需解析默认的字节大小
			if err := cfg.resolveSizes(); err != nil {
				return nil, err
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate 校验并解析配置
func (c *Config) Validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("端口必须在 1-65535 之间，当前: %d", c.Server.Port)
	}
	if c.Limits.MaxFilesPerShare < 1 || c.Limits.MaxFilesPerShare > 100 {
		return fmt.Errorf("单分享文件数必须在 1-100 之间，当前: %d", c.Limits.MaxFilesPerShare)
	}
	if c.Limits.ExpireHours < 1 || c.Limits.ExpireHours > 168 {
		return fmt.Errorf("有效期必须在 1-168 小时之间，当前: %d", c.Limits.ExpireHours)
	}
	if c.Security.RateLimitPerMin < 1 {
		return fmt.Errorf("限流值必须大于 0")
	}
	if c.Security.ShareCodeMaxAttempts < 1 {
		return fmt.Errorf("失败锁定次数必须大于 0")
	}
	if c.Security.ShareCodeLockMinutes < 1 {
		return fmt.Errorf("锁定时长必须大于 0")
	}
	if c.Limits.UploadTTLHours < 1 || c.Limits.UploadTTLHours > 168 {
		return fmt.Errorf("上传会话保留时长必须在 1-168 小时之间，当前: %d", c.Limits.UploadTTLHours)
	}

	if err := c.resolveSizes(); err != nil {
		return err
	}

	// 存储目录转绝对路径
	abs, err := filepath.Abs(c.Storage.Dir)
	if err != nil {
		return fmt.Errorf("无法解析存储目录: %w", err)
	}
	c.Storage.Dir = abs

	if c.Log.Dir != "" {
		absLog, err := filepath.Abs(c.Log.Dir)
		if err != nil {
			return fmt.Errorf("无法解析日志目录: %w", err)
		}
		c.Log.Dir = absLog
	}

	return nil
}

// resolveSizes 解析文件大小字符串为字节
func (c *Config) resolveSizes() error {
	totalBytes, err := ParseSize(c.Storage.MaxTotalSize)
	if err != nil {
		return fmt.Errorf("maxTotalSize 解析失败: %w", err)
	}
	c.Storage.MaxTotalSizeBytes = totalBytes

	fileBytes, err := ParseSize(c.Limits.MaxFileSize)
	if err != nil {
		return fmt.Errorf("maxFileSize 解析失败: %w", err)
	}
	c.Limits.MaxFileSizeBytes = fileBytes

	chunkBytes, err := ParseSize(c.Limits.ChunkSize)
	if err != nil {
		return fmt.Errorf("chunkSize 解析失败: %w", err)
	}
	c.Limits.ChunkSizeBytes = chunkBytes
	return nil
}

// ParseSize 解析形如 "50GB"、"500MB"、"1024B" 的大小字符串为字节数
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("大小字符串为空")
	}

	// 拆分数字与单位：从第一个非数字字符起为单位
	var numStr string
	var unit string
	for i, r := range s {
		if r >= '0' && r <= '9' || r == '.' {
			numStr = s[:i+1]
		} else {
			unit = strings.TrimSpace(s[i:])
			break
		}
	}

	// 若未在循环中跳出（全数字或仅数字+点），numStr 即整体，单位为空
	if unit == "" && numStr == "" {
		return 0, fmt.Errorf("无效的大小: %s", s)
	}

	num, err := strconv.ParseFloat(strings.TrimSpace(numStr), 64)
	if err != nil {
		return 0, fmt.Errorf("无效的数字: %s", numStr)
	}

	var multiplier float64 = 1
	switch strings.ToUpper(unit) {
	case "":
		multiplier = 1
	case "B":
		multiplier = 1
	case "KB", "K":
		multiplier = 1024
	case "MB", "M":
		multiplier = 1024 * 1024
	case "GB", "G":
		multiplier = 1024 * 1024 * 1024
	case "TB", "T":
		multiplier = 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("未知的单位: %s", unit)
	}

	return int64(num * multiplier), nil
}

// ApplyEnv 用环境变量覆盖配置（环境变量优先级最高）
func (c *Config) ApplyEnv() error {
	if v := os.Getenv("FILESYNC_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			c.Server.Port = p
		}
	}
	if v := os.Getenv("FILESYNC_DATA_DIR"); v != "" {
		c.Storage.Dir = v
	}
	if v := os.Getenv("FILESYNC_MAX_TOTAL"); v != "" {
		c.Storage.MaxTotalSize = v
	}
	if v := os.Getenv("FILESYNC_MAX_FILE"); v != "" {
		c.Limits.MaxFileSize = v
	}
	if v := os.Getenv("FILESYNC_MAX_FILES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Limits.MaxFilesPerShare = n
		}
	}
	if v := os.Getenv("FILESYNC_EXPIRE_HOURS"); v != "" {
		if h, err := strconv.Atoi(v); err == nil {
			c.Limits.ExpireHours = h
		}
	}
	if v := os.Getenv("FILESYNC_CHUNK_SIZE"); v != "" {
		c.Limits.ChunkSize = v
	}
	if v := os.Getenv("FILESYNC_UPLOAD_TTL_HOURS"); v != "" {
		if h, err := strconv.Atoi(v); err == nil {
			c.Limits.UploadTTLHours = h
		}
	}
	if v := os.Getenv("FILESYNC_LOG_DIR"); v != "" {
		c.Log.Dir = v
	}
	// 环境变量覆盖后需重新解析大小
	return c.resolveSizes()
}
