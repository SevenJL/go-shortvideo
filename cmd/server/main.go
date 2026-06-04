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
	"shortvideo/internal/rec"
	"shortvideo/internal/relation"
	"shortvideo/internal/store"
	"shortvideo/pkg/metrics"
	"shortvideo/pkg/mq"
	"shortvideo/pkg/mysqlx"
	"shortvideo/pkg/ratelimit"
	"shortvideo/pkg/redisx"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP 监听地址")
	uploadDir := flag.String("upload-dir", "./data/uploads", "视频上传保存目录")
	jwtSecret := flag.String("jwt-secret", "dev-secret-change-in-production", "JWT 签名密钥")
	redisAddr := flag.String("redis", "", "Redis 地址,留空使用内存版")
	mysqlDSN := flag.String("mysql-dsn", "", "MySQL DSN,如 test:123456@tcp(127.0.0.1:3306)/testdb?parseTime=true")
	reconcileThreshold := flag.Int64("reconcile-threshold", 5, "对账偏差阈值(Redis vs MySQL 计数差异)")
	reconcileInterval := flag.Duration("reconcile-interval", 5*time.Minute, "对账间隔")
	seed := flag.Bool("seed", true, "是否注入演示数据")
	flag.Parse()

	// ---- 存储层 ----
	s := store.New()
	if *seed {
		s.Seed()
		log.Println("已注入演示数据: alice(1)/bob(2)/carol(3) + 4 视频; carol 关注 alice、bob; 密码均为 password123")
	}
	if err := os.MkdirAll(*uploadDir, 0o755); err != nil {
		log.Fatalf("创建上传目录失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ---- MySQL（可选）----
	var likeRepo like.Repo
	var relRepo relation.Repo

	if *mysqlDSN != "" {
		db, err := mysqlx.NewDB(mysqlx.Config{DSN: *mysqlDSN})
		if err != nil {
			log.Fatalf("MySQL 连接失败: %v", err)
		}
		if err := mysqlx.RunMigrations(db); err != nil {
			log.Fatalf("MySQL 建表失败: %v", err)
		}
		log.Println("MySQL 已连接,表结构已就绪")

		likeRepo = like.NewMysqlRepo(db)
		relRepo = relation.NewMysqlRepo(db)
	} else {
		// 内存 Repo（like worker 用）
		likeRepo = newMemLikeRepo()
		relRepo = relation.NewMemRepo(s)
		log.Println("MySQL 未配置,使用内存版持久化")
	}

	// ---- Redis（可选）----
	var likeSvc api.LikeService
	var feedSvc *feed.Service
	var fanoutPub api.FanoutPublisher
	var aggregator *like.CounterAggregator
	var reconcile *like.Reconciler

	if *redisAddr != "" {
		rdb := redisx.NewClient(*redisAddr)
		log.Printf("Redis 已连接: %s", *redisAddr)

		// MQ 总线
		bus := mq.NewChanBus(2048)

		// 点赞服务（Redis 版）
		likeProducer := &busProducer{bus: bus, topic: "like-events"}
		redisLikeSvc := like.NewService(rdb, likeProducer)
		likeSvc = api.NewRedisLikeService(redisLikeSvc)

		// 聚合器
		aggregator = like.NewCounterAggregator(rdb)
		go aggregator.Run(ctx, 100*time.Millisecond)

		// 点赞持久化 Worker
		likeConsumer := like.NewConsumer(likeRepo)
		bus.Subscribe("like-events", func(ctx context.Context, payload []byte) error {
			var e like.LikeEvent
			if err := json.Unmarshal(payload, &e); err != nil {
				return nil
			}
			return likeConsumer.HandleBatch(ctx, []like.LikeEvent{e})
		})

		// 关系服务
		relSvc := relation.NewService(rdb, relRepo)

		// 关注流服务（推拉结合）
		feedStore := feed.NewStore(rdb)
		feedSvc = feed.NewService(feedStore, relSvc, &storeHydrator{s: s}, redisLikeSvc)

		// 写扩散 Worker
		fanoutWorker := feed.NewFanoutWorker(feedStore, relSvc)
		bus.Subscribe("fanout", func(ctx context.Context, payload []byte) error {
			var t feed.FanoutTask
			if err := json.Unmarshal(payload, &t); err != nil {
				return nil
			}
			start := time.Now()
			if err := fanoutWorker.Handle(ctx, t); err != nil {
				log.Printf("fanout: 失败 authorID=%d videoID=%d: %v", t.AuthorID, t.VideoID, err)
				return err
			}
			metrics.FanoutHistogram.Observe(time.Since(start).Seconds())
			log.Printf("fanout: 完成 authorID=%d videoID=%d", t.AuthorID, t.VideoID)
			return nil
		})

		// MQ 消费
		go func() {
			if err := bus.Run(ctx); err != nil {
				log.Printf("MQ bus 退出: %v", err)
			}
		}()

		// 写扩散发布器
		fanoutPub = &fanoutPublisher{bus: bus, topic: "fanout"}

		// 对账器（有 MySQL 时启用）
		if mysqlRepo, ok := likeRepo.(*like.MysqlRepo); ok {
			reconcile = like.NewReconcilerWithThreshold(redisLikeSvc, mysqlRepo, rdb, *reconcileInterval, *reconcileThreshold)
			go reconcile.Run(ctx)
		}

		log.Println("已启用推拉结合关注流 + Redis 分片点赞 + 异步持久化")
	} else {
		likeSvc = like.NewMemLikeService(s)
		log.Println("Redis 未配置,使用内存版")
	}

	// ---- 限流器（按接口粒度区分）----
	pathLimiter := ratelimit.NewPathLimiter(ratelimit.DefaultPathRules(),
		ratelimit.New(500, 1000)) // fallback: 500 QPS
	go func() {
		t := time.NewTicker(1 * time.Minute)
		defer t.Stop()
		for range t.C {
			pathLimiter.Cleanup()
		}
	}()

	// ---- 推荐流服务（有 Redis 时启用）----
	var recSvc *rec.Recommender
	if *redisAddr != "" {
		rdb := redisx.NewClient(*redisAddr)
		recStore := rec.NewStore(rdb)
		recSvc = rec.NewRecommender(recStore,
			&recHydrator{s: s},
			&recLikeProvider{svc: likeSvc},
		)

		// 启动时立即用已有视频填充热门/新鲜池
		seedVideos := s.ListVideos(0, 1000)
		for _, v := range seedVideos {
			recStore.UpdateHotScore(context.Background(), v.ID, v.LikeCount, v.CommentCount, v.CreatedAt)
			recStore.AddToFresh(context.Background(), v.ID, time.UnixMilli(v.CreatedAt))
		}
		log.Printf("rec: 初始填充 %d 个视频到热门/新鲜池", len(seedVideos))

		// 后台任务：每 5 分钟刷新热门分数
		go recSvc.RunBackgroundTasks(ctx, 5*time.Minute, func() []rec.VideoStats {
			// 从内存 store 获取所有视频统计
			var stats []rec.VideoStats
			videos := s.ListVideos(0, 1000)
			for _, v := range videos {
				stats = append(stats, rec.VideoStats{
					VideoID: v.ID, LikeCount: v.LikeCount,
					CommentCount: v.CommentCount, CreatedAt: v.CreatedAt,
				})
			}
			return stats
		})

		// 新发布视频加入新鲜池
		go func() {
			t := time.NewTicker(30 * time.Second)
			defer t.Stop()
			for range t.C {
				videos := s.ListVideos(0, 50)
				for _, v := range videos {
					recStore.AddToFresh(context.Background(), v.ID, time.UnixMilli(v.CreatedAt))
				}
			}
		}()

		log.Println("已启用推荐流 (多路召回 + 热度排序)")
	}

	// ---- HTTP 路由 ----
	apiHandler := api.NewRouter(s, *uploadDir, *jwtSecret, likeSvc, feedSvc, recSvc, fanoutPub)

	// 中间件链: metrics → ratelimit → api
	mux := http.NewServeMux()
	mux.Handle("/", apiHandler)
	mux.Handle("GET /metrics", metrics.Handler())

	// 全局中间件包装
	var handler http.Handler = mux
	handler = metrics.Middleware(handler)                         // 最外层: 记录指标
	handler = pathLimiter.Middleware(ratelimit.KeyFromUserID)(handler) // 按用户+路径粒度限流

	srv := &http.Server{
		Addr:         *addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("短视频服务已启动,监听 %s (http://localhost%s/healthz, http://localhost%s/metrics)", *addr, *addr, *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	// 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("正在关闭服务...")
	cancel()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Fatalf("关闭异常: %v", err)
	}
	log.Println("服务已退出")
}

// ---- 适配器 ----

type busProducer struct {
	bus   *mq.ChanBus
	topic string
}

func (p *busProducer) Publish(_ context.Context, e like.LikeEvent) error {
	data, _ := json.Marshal(e)
	return p.bus.Publish(context.Background(), p.topic, data)
}

type fanoutPublisher struct {
	bus   *mq.ChanBus
	topic string
}

func (p *fanoutPublisher) PublishFanout(authorID, videoID, tsMilli int64) {
	task := feed.FanoutTask{AuthorID: authorID, VideoID: videoID, TsMilli: tsMilli}
	data, _ := json.Marshal(task)
	_ = p.bus.Publish(context.Background(), p.topic, data)
}

// recHydrator 将内存 Store 适配为 rec.VideoProvider。
type recHydrator struct{ s *store.Store }

func (h *recHydrator) BatchGet(_ context.Context, ids []int64) ([]rec.VideoVO, error) {
	out := make([]rec.VideoVO, 0, len(ids))
	for _, id := range ids {
		v, err := h.s.GetVideo(id)
		if err != nil {
			continue
		}
		out = append(out, rec.VideoVO{
			VideoID: v.ID, AuthorID: v.AuthorID, Title: v.Title,
			PlayURL: v.PlayURL, CoverURL: v.CoverURL,
			CreatedAt: v.CreatedAt, LikeCount: v.LikeCount, CommentCount: v.CommentCount,
		})
	}
	return out, nil
}

// recLikeProvider 将 api.LikeService 适配为 rec.LikeStateProvider。
type recLikeProvider struct{ svc api.LikeService }

func (p *recLikeProvider) BatchIsLiked(ctx context.Context, uid int64, vids []int64) (map[int64]bool, error) {
	return p.svc.BatchIsLiked(ctx, uid, vids)
}

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

// ---- 内存 Repo（MySQL 未配置时的 fallback）----

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
