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
	"shortvideo/pkg/mysqlx"
	"shortvideo/pkg/redisx"
)

func main() {
	redisAddr := flag.String("redis", envOrDefault("REDIS_ADDR", "localhost:6379"), "Redis 地址")
	mysqlDSN := flag.String("mysql-dsn", envOrDefault("MYSQL_DSN", ""), "MySQL DSN")
	mqType := flag.String("mq-type", envOrDefault("MQ_TYPE", "chan"), "MQ 类型: chan|redis_stream")
	flag.Parse()

	var repo like.Repo
	if *mysqlDSN != "" {
		db, err := mysqlx.NewDB(mysqlx.Config{DSN: *mysqlDSN, MaxOpenConns: 10, MaxIdleConns: 2})
		if err != nil {
			log.Fatalf("MySQL 连接失败: %v", err)
		}
		defer db.Close()
		if err := mysqlx.RunMigrations(db); err != nil {
			log.Fatalf("MySQL 建表失败: %v", err)
		}
		repo = like.NewMysqlRepo(db)
		log.Println("MySQL 点赞仓储已启用")
	} else {
		repo = newMemLikeRepo()
		log.Println("使用内存点赞仓储（仅开发/测试）")
	}
	consumer := like.NewConsumer(repo)

	var bus mq.Consumer
	if *mqType == "redis_stream" {
		rdb := redisx.NewClient(*redisAddr)
		bus = mq.NewRedisStreamBus(rdb, "shortvideo", 3)
		log.Printf("使用 Redis Stream 消费 like-events: %s", *redisAddr)
	} else {
		bus = mq.NewChanBus(2048)
		log.Println("使用本地 ChanBus（仅开发/测试）")
	}
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
