// cmd/fanout 是写扩散 Worker 入口。
// 消费 MQ 中的 FanoutTask，分页拉粉丝，批量 Pipeline 写收件箱。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"shortvideo/internal/feed"
	"shortvideo/internal/relation"
	"shortvideo/internal/store"
	"shortvideo/pkg/mq"
	"shortvideo/pkg/redisx"
)

func main() {
	redisAddr := flag.String("redis", envOrDefault("REDIS_ADDR", "localhost:6379"), "Redis 地址")
	flag.Parse()

	rdb := redisx.NewClient(*redisAddr)
	memStore := store.New()
	memStore.Seed()

	relRepo := relation.NewMemRepo(memStore)
	relSvc := relation.NewService(rdb, relRepo)
	feedStore := feed.NewStore(rdb)
	worker := feed.NewFanoutWorker(feedStore, relSvc)

	bus := mq.NewChanBus(2048)
	bus.Subscribe("fanout", func(ctx context.Context, payload []byte) error {
		var task feed.FanoutTask
		if err := json.Unmarshal(payload, &task); err != nil {
			log.Printf("fanout: 解析任务失败: %v", err)
			return nil // 解析失败不重试，避免死循环
		}
		if err := worker.Handle(ctx, task); err != nil {
			log.Printf("fanout: 处理任务失败 authorID=%d videoID=%d: %v", task.AuthorID, task.VideoID, err)
			return err // 返回 err 触发 MQ 重试
		}
		log.Printf("fanout: 完成 authorID=%d videoID=%d", task.AuthorID, task.VideoID)
		return nil
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Println("写扩散 Worker 启动，等待任务...")
	if err := bus.Run(ctx); err != nil {
		log.Printf("Worker 退出: %v", err)
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
