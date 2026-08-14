package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"

	"filesync/internal/config"
	"filesync/internal/server"
)

//go:embed all:web
var webFS embed.FS

func main() {
	cfgPath := flag.String("config", "config.yaml", "配置文件路径")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}
	if err := cfg.ApplyEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "应用环境变量失败: %v\n", err)
		os.Exit(1)
	}

	// 注入前端资源
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("加载前端资源失败: %v", err)
	}
	server.SetWebFS(sub)

	srv, err := server.New(cfg)
	if err != nil {
		log.Fatalf("初始化服务器失败: %v", err)
	}

	if err := srv.Start(); err != nil {
		log.Fatalf("服务器运行失败: %v", err)
	}
}
