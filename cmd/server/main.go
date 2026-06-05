package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

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
	mysqlDSN := flag.String("mysql-dsn", "", "MySQL DSN")
	reconcileThreshold := flag.Int64("reconcile-threshold", 5, "对账偏差阈值")
	reconcileInterval := flag.Duration("reconcile-interval", 5*time.Minute, "对账间隔")
	seed := flag.Bool("seed", true, "是否注入演示数据")
	flag.Parse()

	// ---- 存储层 ----
	s := store.New()
	if *seed {
		s.Seed()
		log.Println("已注入演示数据: alice(1)/bob(2)/carol(3); 密码均为 password123")
	}
	if err := os.MkdirAll(*uploadDir, 0o755); err != nil {
		log.Fatalf("创建上传目录失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ---- MySQL ----
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
		likeRepo = like.NewMysqlRepo(db)
		relRepo = relation.NewMysqlRepo(db)
		log.Println("MySQL 已连接")
	} else {
		likeRepo = newMemLikeRepo()
		relRepo = relation.NewMemRepo(s)
	}

	// ---- Redis + 推荐流 ----
	var likeSvc api.LikeService
	var feedSvc *feed.Service
	var recSvc *rec.Recommender
	var fanoutPub api.FanoutPublisher
	var reconcile *like.Reconciler

	if *redisAddr != "" {
		rdb := redisx.NewClient(*redisAddr)
		log.Printf("Redis 已连接: %s", *redisAddr)

		bus := mq.NewChanBus(2048)
		likeProducer := &busProducer{bus: bus, topic: "like-events"}
		redisLikeSvc := like.NewService(rdb, likeProducer)
		likeSvc = api.NewRedisLikeService(redisLikeSvc)

		aggregator := like.NewCounterAggregator(rdb)
		go aggregator.Run(ctx, 100*time.Millisecond)

		likeConsumer := like.NewConsumer(likeRepo)
		bus.Subscribe("like-events", func(ctx context.Context, payload []byte) error {
			var e like.LikeEvent
			if err := json.Unmarshal(payload, &e); err != nil {
				return nil
			}
			return likeConsumer.HandleBatch(ctx, []like.LikeEvent{e})
		})

		relSvc := relation.NewService(rdb, relRepo)
		feedStore := feed.NewStore(rdb)
		feedSvc = feed.NewService(feedStore, relSvc, &storeHydrator{s: s}, redisLikeSvc)

		fanoutWorker := feed.NewFanoutWorker(feedStore, relSvc)
		bus.Subscribe("fanout", func(ctx context.Context, payload []byte) error {
			var t feed.FanoutTask
			if err := json.Unmarshal(payload, &t); err != nil {
				return nil
			}
			start := time.Now()
			if err := fanoutWorker.Handle(ctx, t); err != nil {
				return err
			}
			metrics.FanoutHistogram.Observe(time.Since(start).Seconds())
			return nil
		})

		go func() { _ = bus.Run(ctx) }()
		fanoutPub = &fanoutPublisher{bus: bus, topic: "fanout"}

		if mysqlRepo, ok := likeRepo.(*like.MysqlRepo); ok {
			reconcile = like.NewReconcilerWithThreshold(redisLikeSvc, mysqlRepo, rdb, *reconcileInterval, *reconcileThreshold)
			go reconcile.Run(ctx)
		}

		// 推荐流
		recStore := rec.NewStore(rdb)
		recSvc = rec.NewRecommender(recStore,
			&recHydrator{s: s}, &recLikeProvider{svc: likeSvc},
		)
		videos := s.ListVideos(0, 1000)
		for _, v := range videos {
			recStore.UpdateHotScore(context.Background(), v.ID, v.LikeCount, v.CommentCount, v.CreatedAt)
			recStore.AddToFresh(context.Background(), v.ID, time.UnixMilli(v.CreatedAt))
		}
		go recSvc.RunBackgroundTasks(ctx, 5*time.Minute, func() []rec.VideoStats {
			var stats []rec.VideoStats
			for _, v := range s.ListVideos(0, 1000) {
				stats = append(stats, rec.VideoStats{VideoID: v.ID, LikeCount: v.LikeCount, CommentCount: v.CommentCount, CreatedAt: v.CreatedAt})
			}
			return stats
		})
		log.Println("已启用推荐流 + 推拉结合关注流 + Redis 分片点赞")
	} else {
		likeSvc = like.NewMemLikeService(s)
		log.Println("Redis 未配置,使用内存版")
	}

	// ---- 限流 ----
	pathLimiter := ratelimit.NewPathLimiter(ratelimit.DefaultPathRules(), ratelimit.New(500, 1000))
	go func() {
		t := time.NewTicker(1 * time.Minute)
		defer t.Stop()
		for range t.C { pathLimiter.Cleanup() }
	}()

	// ---- Gin 路由 ----
	gin.SetMode(gin.ReleaseMode)
	r := api.NewRouter(s, *uploadDir, *jwtSecret, likeSvc, feedSvc, recSvc, fanoutPub)
	r.Use(metrics.GinMiddleware())
	r.Use(pathLimiter.GinMiddleware(ratelimit.KeyFromUserID))
	r.GET("/metrics", func(c *gin.Context) { metrics.Handler().ServeHTTP(c.Writer, c.Request) })

	srv := &http.Server{Addr: *addr, Handler: r, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}

	go func() {
		log.Printf("短视频服务已启动(Gin),监听 %s", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

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

func (p *busProducer) Publish(ctx context.Context, e like.LikeEvent) error {
	data, _ := json.Marshal(e)
	return p.bus.Publish(ctx, p.topic, data)
}

type fanoutPublisher struct {
	bus   *mq.ChanBus
	topic string
}

func (p *fanoutPublisher) PublishFanout(authorID, videoID, tsMilli int64) {
	task := feed.FanoutTask{AuthorID: authorID, VideoID: videoID, TsMilli: tsMilli}
	data, _ := json.Marshal(task)
	if err := p.bus.Publish(context.Background(), p.topic, data); err != nil {
		log.Printf("fanout publish failed: authorID=%d videoID=%d err=%v", authorID, videoID, err)
	}
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
			VideoID: v.ID, AuthorID: v.AuthorID, Title: v.Title,
			PlayURL: v.PlayURL, CoverURL: v.CoverURL,
			CreatedAt: v.CreatedAt, LikeCount: v.LikeCount,
		})
	}
	return out, nil
}

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

type recLikeProvider struct{ svc api.LikeService }

func (p *recLikeProvider) BatchIsLiked(ctx context.Context, uid int64, vids []int64) (map[int64]bool, error) {
	return p.svc.BatchIsLiked(ctx, uid, vids)
}

// ---- 内存 Repo ----

type likeRecord struct {
	liked     bool
	updatedAt int64
}

type memLikeRepo struct {
	mu      sync.Mutex
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
	r.mu.Lock()
	defer r.mu.Unlock()
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
