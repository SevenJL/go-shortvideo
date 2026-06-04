package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"shortvideo/internal/api"
	"shortvideo/internal/feed"
	"shortvideo/internal/like"
	"shortvideo/internal/relation"
	"shortvideo/internal/store"
	"shortvideo/pkg/mq"
	"shortvideo/pkg/redisx"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP 监听地址,如 :8080")
	uploadDir := flag.String("upload-dir", "./data/uploads", "视频上传保存目录")
	jwtSecret := flag.String("jwt-secret", "dev-secret-change-in-production", "JWT 签名密钥(生产环境务必更换)")
	redisAddr := flag.String("redis", "", "Redis 地址,留空则使用内存版(无需外部依赖)")
	seed := flag.Bool("seed", true, "是否注入演示数据")
	flag.Parse()

	// ---- 存储层 ----
	s := store.New()
	if *seed {
		s.Seed()
		log.Println("已注入演示数据:用户 alice(1)/bob(2)/carol(3) + 4 个视频;carol 关注了 alice、bob;密码均为 password123")
	}

	if err := os.MkdirAll(*uploadDir, 0o755); err != nil {
		log.Fatalf("创建上传目录失败: %v", err)
	}

	// ---- 点赞服务 ----
	var likeSvc api.LikeService
	var feedSvc *feed.Service
	var fanoutPub api.FanoutPublisher
	var aggregator *like.CounterAggregator
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if *redisAddr != "" {
		// 有 Redis → 使用 Redis 版点赞服务 + 关注流服务
		rdb := redisx.NewClient(*redisAddr)
		log.Printf("Redis 已连接: %s, 启用分片计数 + 推拉结合关注流", *redisAddr)

		// MQ 总线（进程内 channel 实现，生产可替换为 Kafka）
		bus := mq.NewChanBus(2048)

		// 点赞服务（Redis 去重门 + 分片计数 + 异步持久化）
		likeProducer := &busProducer{bus: bus, topic: "like-events"}
		redisLikeSvc := like.NewService(rdb, likeProducer)
		likeSvc = api.NewRedisLikeService(redisLikeSvc)

		// 本地聚合器（内存聚合计数增量，每 100ms 刷 Redis）
		aggregator = like.NewCounterAggregator(rdb)
		go aggregator.Run(ctx, 100*time.Millisecond)

		// 点赞持久化 Worker（消费 MQ 事件落库，此处用内存 Repo）
		likeConsumer := like.NewConsumer(newMemLikeRepo())
		bus.Subscribe("like-events", func(ctx context.Context, payload []byte) error {
			var e like.LikeEvent
			if err := json.Unmarshal(payload, &e); err != nil {
				return nil // 解析失败不重试
			}
			return likeConsumer.HandleBatch(ctx, []like.LikeEvent{e})
		})

		// 关系服务（大 V 判定 + 分页拉粉丝，使用内存 Repo）
		relRepo := relation.NewMemRepo(s)
		relSvc := relation.NewService(rdb, relRepo)

		// 关注流服务（推拉结合）
		feedStore := feed.NewStore(rdb)
		feedSvc = feed.NewService(feedStore, relSvc, &storeHydrator{s: s}, redisLikeSvc)

		// 写扩散 Worker（消费 fanout 任务）
		fanoutWorker := feed.NewFanoutWorker(feedStore, relSvc)
		bus.Subscribe("fanout", func(ctx context.Context, payload []byte) error {
			var t feed.FanoutTask
			if err := json.Unmarshal(payload, &t); err != nil {
				return nil
			}
			if err := fanoutWorker.Handle(ctx, t); err != nil {
				log.Printf("fanout: 处理失败 authorID=%d videoID=%d: %v", t.AuthorID, t.VideoID, err)
				return err
			}
			log.Printf("fanout: 完成 authorID=%d videoID=%d", t.AuthorID, t.VideoID)
			return nil
		})

		// 后台运行 MQ 消费
		go func() {
			if err := bus.Run(ctx); err != nil {
				log.Printf("MQ bus 退出: %v", err)
			}
		}()

		// 写扩散发布器
		fanoutPub = &fanoutPublisher{bus: bus, topic: "fanout"}

		log.Println("已启用推拉结合关注流 + Redis 分片点赞")
	} else {
		// 无 Redis → 使用内存版（零外部依赖，适合开发/演示）
		likeSvc = like.NewMemLikeService(s)
		// feedSvc 为 nil，关注流走 store.FollowingFeed（纯拉模型）
		// fanoutPub 为 nil，发布视频不触发写扩散
		log.Println("Redis 未配置,使用内存版点赞(单锁) + 纯拉模型关注流")
	}

	// ---- HTTP 服务 ----
	srv := &http.Server{
		Addr:         *addr,
		Handler:      api.NewRouter(s, *uploadDir, *jwtSecret, likeSvc, feedSvc, fanoutPub),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		mode := "内存版"
		if *redisAddr != "" {
			mode = "Redis 版"
		}
		log.Printf("短视频服务已启动(%s),监听 %s (本地访问 http://localhost%s/healthz)", mode, *addr, *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	// 等待中断信号,优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("正在关闭服务...")
	cancel() // 停止 aggregator + MQ consumer
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Fatalf("关闭异常: %v", err)
	}
	log.Println("服务已退出")
}

// ---- 适配器 ----

// busProducer 将 mq.ChanBus 适配为 like.EventProducer。
type busProducer struct {
	bus   *mq.ChanBus
	topic string
}

func (p *busProducer) Publish(_ context.Context, e like.LikeEvent) error {
	data, _ := json.Marshal(e)
	return p.bus.Publish(context.Background(), p.topic, data)
}

// fanoutPublisher 实现 api.FanoutPublisher，投递写扩散任务到 MQ。
type fanoutPublisher struct {
	bus   *mq.ChanBus
	topic string
}

func (p *fanoutPublisher) PublishFanout(authorID, videoID, tsMilli int64) {
	task := feed.FanoutTask{AuthorID: authorID, VideoID: videoID, TsMilli: tsMilli}
	data, _ := json.Marshal(task)
	_ = p.bus.Publish(context.Background(), p.topic, data)
}

// storeHydrator 将内存 Store 适配为 feed.VideoHydrator。
type storeHydrator struct{ s *store.Store }

func (h *storeHydrator) BatchGetVisible(_ context.Context, ids []int64) ([]feed.VideoVO, error) {
	out := make([]feed.VideoVO, 0, len(ids))
	for _, id := range ids {
		v, err := h.s.GetVideo(id)
		if err != nil {
			continue
		}
		out = append(out, feed.VideoVO{
			VideoID:   v.ID,
			AuthorID:  v.AuthorID,
			Title:     v.Title,
			PlayURL:   v.PlayURL,
			CoverURL:  v.CoverURL,
			CreatedAt: v.CreatedAt,
			LikeCount: v.LikeCount,
		})
	}
	return out, nil
}

// ---- 内存 Repo（like worker 持久化用，生产替换为 MySQL） ----

type likeRecord struct {
	liked     bool
	updatedAt int64
}

type memLikeRepo struct {
	records map[[2]int64]*likeRecord
	stats   map[int64]int64
}

func newMemLikeRepo() *memLikeRepo {
	return &memLikeRepo{
		records: make(map[[2]int64]*likeRecord),
		stats:   make(map[int64]int64),
	}
}

func (r *memLikeRepo) UpsertLike(_ context.Context, uid, vid, ts int64, liked bool) error {
	key := [2]int64{uid, vid}
	if rec := r.records[key]; rec == nil {
		r.records[key] = &likeRecord{liked: liked, updatedAt: ts}
	} else if ts > rec.updatedAt {
		rec.liked = liked
		rec.updatedAt = ts
	}
	return nil
}

func (r *memLikeRepo) ApplyCountDeltas(_ context.Context, deltas map[int64]int64) error {
	for vid, delta := range deltas {
		r.stats[vid] += delta
		if r.stats[vid] < 0 {
			r.stats[vid] = 0
		}
	}
	return nil
}
