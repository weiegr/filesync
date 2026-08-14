package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"filesync/internal/config"
	"filesync/internal/handler"
	"filesync/internal/middleware"
	"filesync/internal/service"
	"filesync/internal/store"

	"github.com/gin-gonic/gin"
)

// SetWebFS 由 main 包在启动前注入 embed 的前端资源
var webFS fs.FS

// SetWebFS 注入前端资源（main 包 //go:embed 后调用）
func SetWebFS(fsys fs.FS) { webFS = fsys }

// Server 组合所有依赖，提供 New / Start
type Server struct {
	cfg       *config.Config
	httpSrv   *http.Server
	cleanup   *service.Cleanup
	stopClean func()
	logger    *log.Logger
}

func New(cfg *config.Config) (*Server, error) {
	// 日志
	if err := os.MkdirAll(cfg.Log.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}
	logFile, err := os.OpenFile(cfg.Log.Dir+"/app.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, fmt.Errorf("打开日志文件失败: %w", err)
	}
	logger := log.New(logFile, "", log.LstdFlags|log.Lshortfile)

	// 数据库
	dbPath := cfg.Storage.Dir + "/../app.db"
	absDbPath, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, fmt.Errorf("解析数据库路径失败: %w", err)
	}
	db, err := store.Open(absDbPath)
	if err != nil {
		return nil, err
	}

	// store / service / handler
	shareStore := store.NewShareStore(db)
	fileStore, err := store.NewFileStore(cfg.Storage.Dir)
	if err != nil {
		return nil, err
	}
	codeGen := service.NewCodeGenerator(shareStore)
	shareSvc := service.NewShareService(shareStore, fileStore, codeGen, cfg)

	// 分片上传：临时目录与正式分享目录隔离
	uploadsDir := filepath.Join(filepath.Dir(cfg.Storage.Dir), "uploads")
	uploadStore, err := store.NewUploadStore(uploadsDir)
	if err != nil {
		return nil, err
	}
	uploadRepo := store.NewUploadRepo(db)
	uploadSvc := service.NewUploadService(uploadRepo, uploadStore, fileStore, shareStore, codeGen, cfg)

	attempt := middleware.NewAttemptStore(db, cfg.Security.ShareCodeMaxAttempts, cfg.Security.ShareCodeLockMinutes)
	rl := middleware.NewRateLimiter(cfg.Security.RateLimitPerMin)

	shareH := handler.NewShareHandler(shareSvc, cfg, attempt)
	uploadH := handler.NewUploadHandler(uploadSvc, cfg)
	healthH := handler.NewHealthHandler()

	// 前端 embed（由 main 包注入）
	if webFS == nil {
		return nil, fmt.Errorf("前端资源未注入，请调用 SetWebFS")
	}
	staticH, err := handler.NewStaticHandlerFS(webFS)
	if err != nil {
		return nil, err
	}

	// gin
	if cfg.Server.TrustProxy {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.RecoveryWithWriter(logFile, gin.RecoveryFunc(func(c *gin.Context, recovered interface{}) {
		logger.Printf("[PANIC] %v", recovered)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "服务器内部错误"})
	})))
	r.Use(gin.LoggerWithWriter(logFile))
	r.Use(middleware.SecurityHeaders())

	// 信任代理（nginx 后）
	_ = r.SetTrustedProxies([]string{"127.0.0.1", "::1"})

	// 静态页面
	r.GET("/", staticH.Index)
	r.GET("/upload", staticH.Upload)
	r.GET("/s/:code", staticH.ShareList)
	// 静态资源 (图标字体等)
	r.GET("/vendor/*filepath", staticH.Asset)

	// API
	api := r.Group("/api", rl.Handle)
	{
		api.POST("/share", shareH.CreateShare)
		api.GET("/share/:code", attempt.LockGuard(), shareH.GetShare)
		api.GET("/share/:code/files/:fileID", shareH.DownloadFile)

		// 分片上传
		api.POST("/upload/init", uploadH.Init)
		api.POST("/upload/:id/chunk", uploadH.Chunk)
		api.GET("/upload/:id/status", uploadH.Status)
		api.POST("/upload/complete", uploadH.Complete)
	}

	// 健康检查（不限流，便于 deploy 自检）
	r.GET("/health", healthH.Health)

	// 清理 goroutine
	cleanup := service.NewCleanup(shareStore, fileStore, logger)
	cleanup.SetUploadCleanup(uploadSvc, cfg.Limits.UploadTTLHours)

	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		// 大文件上传/下载不设写超时，由 nginx 控制
	}

	return &Server{
		cfg:     cfg,
		httpSrv: httpSrv,
		cleanup: cleanup,
		logger:  logger,
	}, nil
}

func (s *Server) Start() error {
	s.stopClean = s.cleanup.Start()

	// 信号监听
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
		s.logger.Println("[server] 收到停止信号，开始优雅关闭...")
		ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Server.ShutdownTimeout)
		defer cancel()
		if err := s.httpSrv.Shutdown(ctx); err != nil {
			s.logger.Printf("[server] 优雅关闭失败: %v", err)
		}
		if s.stopClean != nil {
			s.stopClean()
		}
	}()

	s.logger.Printf("[server] 监听 :%d", s.cfg.Server.Port)
	if err := s.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	s.logger.Println("[server] 已退出")
	return nil
}
