// cmd/likeworker 是点赞持久化 Worker 入口。
// 消费 MQ 中的 LikeEvent，批量落库（like_record + video_stats）。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"shortvideo/internal/like"
	"shortvideo/pkg/mq"
)

func main() {
	flag.Parse()

	// MemLikeRepo：开发环境的内存实现，生产替换为 MySQL 分片实现。
	repo := newMemLikeRepo()
	consumer := like.NewConsumer(repo)

	bus := mq.NewChanBus(2048)
	bus.Subscribe("like-events", func(ctx context.Context, payload []byte) error {
		var e like.LikeEvent
		if err := json.Unmarshal(payload, &e); err != nil {
			log.Printf("likeworker: 解析事件失败: %v", err)
			return nil
		}
		if err := consumer.HandleBatch(ctx, []like.LikeEvent{e}); err != nil {
			log.Printf("likeworker: 落库失败: %v", err)
			return err
		}
		return nil
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Println("点赞持久化 Worker 启动，等待事件...")
	if err := bus.Run(ctx); err != nil {
		log.Printf("Worker 退出: %v", err)
	}
}

// ---------------------------------------------------------------
// MemLikeRepo：内存 Repo，用于开发/测试
// ---------------------------------------------------------------

type likeRecord struct {
	liked     bool
	updatedAt int64
}

type memLikeRepo struct {
	mu      sync.Mutex
	records map[[2]int64]*likeRecord // (userID, videoID) -> record
	stats   map[int64]int64          // videoID -> like_count
}

func newMemLikeRepo() *memLikeRepo {
	return &memLikeRepo{
		records: make(map[[2]int64]*likeRecord),
		stats:   make(map[int64]int64),
	}
}

func (r *memLikeRepo) UpsertLike(_ context.Context, uid, vid, ts int64, liked bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := [2]int64{uid, vid}
	rec := r.records[key]
	if rec == nil {
		r.records[key] = &likeRecord{liked: liked, updatedAt: ts}
		return nil
	}
	// 只更新时间戳更新的记录，防止乱序消息覆盖最新状态
	if ts > rec.updatedAt {
		rec.liked = liked
		rec.updatedAt = ts
	}
	return nil
}

func (r *memLikeRepo) ApplyCountDeltas(_ context.Context, deltas map[int64]int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for vid, delta := range deltas {
		r.stats[vid] += delta
		if r.stats[vid] < 0 {
			r.stats[vid] = 0
		}
	}
	return nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
