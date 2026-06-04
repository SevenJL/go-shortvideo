package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"shortvideo/internal/api"
	"shortvideo/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP 监听地址,如 :8080")
	uploadDir := flag.String("upload-dir", "./data/uploads", "视频上传保存目录")
	seed := flag.Bool("seed", true, "是否注入演示数据")
	flag.Parse()

	s := store.New()
	if *seed {
		s.Seed()
		log.Println("已注入演示数据:用户 alice(1)/bob(2)/carol(3) + 4 个视频;carol 关注了 alice、bob")
	}

	if err := os.MkdirAll(*uploadDir, 0o755); err != nil {
		log.Fatalf("创建上传目录失败: %v", err)
	}

	srv := &http.Server{
		Addr:         *addr,
		Handler:      api.NewRouter(s, *uploadDir),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("短视频服务已启动,监听 %s(本地访问 http://localhost%s/healthz)", *addr, *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	// 等待中断信号,优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("正在关闭服务...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("关闭异常: %v", err)
	}
	log.Println("服务已退出")
}
