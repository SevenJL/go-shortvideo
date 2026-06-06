package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"golang.org/x/time/rate"

	"shortvideo/internal/api"
	"shortvideo/internal/feed"
	"shortvideo/internal/like"
	"shortvideo/internal/model"
	"shortvideo/internal/rec"
	"shortvideo/internal/relation"
	"shortvideo/internal/store"
	"shortvideo/internal/transcode"
	"shortvideo/pkg/audit"
	"shortvideo/pkg/config"
	"shortvideo/pkg/media"
	"shortvideo/pkg/metrics"
	"shortvideo/pkg/mq"
	"shortvideo/pkg/mysqlx"
	"shortvideo/pkg/oss"
	"shortvideo/pkg/ratelimit"
	"shortvideo/pkg/redisx"
	"shortvideo/pkg/security"
	"shortvideo/pkg/tracing"
)

func main() {
	configFile := flag.String("config", "config.yaml", "配置文件路径")
	flag.Parse()

	// ---- 加载配置 ----
	cfg, err := config.Load(*configFile)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	if err := validateProductionConfig(cfg); err != nil {
		log.Fatalf("生产配置校验失败: %v", err)
	}

	gin.SetMode(cfg.Server.Mode)
	audit.Configure("shortvideo")

	// ---- 存储层 ----
	s := store.New()
	if err := os.MkdirAll(cfg.Storage.UploadDir, 0755); err != nil {
		log.Fatalf("创建上传目录失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shutdownTracing, err := tracing.Init(ctx, "shortvideo")
	if err != nil {
		log.Fatalf("OpenTelemetry 初始化失败: %v", err)
	}
	defer func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutCancel()
		_ = shutdownTracing(shutCtx)
	}()

	// ---- MySQL ----
	var db *sql.DB
	var likeRepo like.Repo
	var relRepo relation.Repo
	if cfg.MysqlEnabled() {
		db, err = mysqlx.NewDB(mysqlx.Config{
			DSN: cfg.MySQL.DSN, MaxOpenConns: cfg.MySQL.MaxOpenConns,
			MaxIdleConns: cfg.MySQL.MaxIdleConns,
		})
		if err != nil {
			log.Fatalf("MySQL 连接失败: %v", err)
		}
		if err := mysqlx.RunMigrations(db); err != nil {
			log.Fatalf("MySQL 建表失败: %v", err)
		}
		likeRepo = like.NewMysqlRepo(db)
		relRepo = relation.NewMysqlRepo(db)
		s = store.NewMySQL(db)
		log.Println("MySQL 已连接")
	} else {
		likeRepo = newMemLikeRepo()
		relRepo = relation.NewMemRepo(s)
	}
	if cfg.Features.Seed {
		s.Seed()
		log.Println("已注入演示数据: alice(1)/bob(2)/carol(3); 密码均为 password123")
	}

	var ossClient *oss.Client
	if cfg.OSSEnabled() {
		ossClient, err = oss.New(oss.Config{
			Endpoint:        cfg.OSS.Endpoint,
			AccessKeyID:     cfg.OSS.AccessKey,
			AccessKeySecret: cfg.OSS.SecretKey,
			BucketName:      cfg.OSS.Bucket,
			CDNDomain:       cfg.OSS.CDNDomain,
			LocalDir:        cfg.Storage.UploadDir,
		})
		if err != nil {
			log.Fatalf("OSS 初始化失败: %v", err)
		}
		log.Println("OSS 客户端已初始化")
	}

	// ---- 中间件组件 ----
	var (
		likeSvc      api.LikeService
		feedSvc      *feed.Service
		recSvc       *rec.Recommender
		fanoutPub    api.FanoutPublisher
		transcodePub api.TranscodePublisher
		rdb          redis.UniversalClient
	)

	if cfg.RedisEnabled() {
		rdb = redisx.NewClient(cfg.Redis.Addr)
		log.Printf("Redis 已连接: %s", cfg.Redis.Addr)

		var bus interface {
			mq.Producer
			mq.Consumer
		}
		if cfg.MQ.Type == "redis_stream" {
			bus = mq.NewRedisStreamBus(rdb, "shortvideo", 3)
		} else {
			bus = mq.NewChanBus(2048)
		}

		// 点赞服务
		likeProducer := &busProducer{bus: bus, topic: "like-events"}
		redisLikeSvc := like.NewService(rdb, likeProducer)
		likeSvc = api.NewRedisLikeService(redisLikeSvc)

		aggregator := like.NewCounterAggregator(rdb)
		redisLikeSvc.SetCounterWriter(aggregator)
		go aggregator.Run(ctx, 100*time.Millisecond)

		// 点赞持久化 Worker
		if cfg.Features.MQEnabled {
			likeConsumer := like.NewConsumer(likeRepo)
			bus.Subscribe("like-events", func(ctx context.Context, payload []byte) error {
				var e like.LikeEvent
				if err := json.Unmarshal(payload, &e); err != nil {
					return nil
				}
				return likeConsumer.HandleBatch(ctx, []like.LikeEvent{e})
			})
		}

		// 关系服务 + 关注流
		relSvc := relation.NewService(rdb, relRepo)
		feedStore := feed.NewStore(rdb)
		feedSvc = feed.NewService(feedStore, relSvc, &storeHydrator{s: s}, redisLikeSvc)

		// 写扩散 Worker
		if cfg.Features.MQEnabled {
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
		}

		// 转码 Worker
		if cfg.Features.MQEnabled && cfg.Features.TranscodeEnabled && media.Available() {
			tw := transcode.NewWorker(cfg.Storage.UploadDir, ossClient, &storeStatusUpdater{s: s})
			bus.Subscribe("transcode", func(ctx context.Context, payload []byte) error {
				var t transcode.Task
				if err := json.Unmarshal(payload, &t); err != nil {
					return nil
				}
				return tw.Handle(ctx, t)
			})
			log.Println("ffmpeg 可用，转码 Worker 已就绪")
		}

		if cfg.Features.MQEnabled {
			go func() { _ = bus.Run(ctx) }()
		}

		fanoutPub = &fanoutPublisher{bus: bus, topic: "fanout", feedStore: feedStore}
		transcodePub = &transcodePublisherAdapter{bus: bus}

		// 对账器
		if cfg.Features.ReconcileEnabled {
			if mysqlRepo, ok := likeRepo.(*like.MysqlRepo); ok {
				reconciler := like.NewReconcilerWithThreshold(redisLikeSvc, mysqlRepo, rdb,
					cfg.Reconcile.Interval, cfg.Reconcile.Threshold)
				go reconciler.Run(ctx)
				log.Printf("对账器已启动 (阈值=%d 间隔=%v)", cfg.Reconcile.Threshold, cfg.Reconcile.Interval)
			}
		}

		// 推荐流
		if cfg.Features.RecommendEnabled {
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
					stats = append(stats, rec.VideoStats{VideoID: v.ID, LikeCount: v.LikeCount,
						CommentCount: v.CommentCount, CreatedAt: v.CreatedAt})
				}
				return stats
			})
			log.Println("推荐流已启用")
		}

		log.Println("已启用: 推拉结合关注流 + Redis分片点赞" +
			logFlag(cfg.Features.MQEnabled, " +MQ") +
			logFlag(cfg.Features.TranscodeEnabled && media.Available(), " +转码") +
			logFlag(cfg.Features.ReconcileEnabled, " +对账") +
			logFlag(cfg.Features.RecommendEnabled, " +推荐流"))
	} else {
		likeSvc = like.NewMemLikeService(s)
		log.Println("Redis 未配置,使用内存版" +
			logFlag(cfg.Features.RecommendEnabled, " (推荐流不可用:需Redis)"))
	}

	// ---- 限流 ----
	var pathLimiter ratelimit.GinLimiter
	if rdb != nil {
		pathLimiter = ratelimit.NewRedisPathLimiter(
			rdb,
			cfg.RateLimitRules(),
			[2]int{cfg.RateLimit.Fallback.QPS, cfg.RateLimit.Fallback.Burst},
		)
		log.Println("已启用 Redis 分布式限流")
	} else {
		localLimiter := ratelimit.NewPathLimiter(cfg.RateLimitRules(),
			ratelimit.New(rate.Limit(cfg.RateLimit.Fallback.QPS), cfg.RateLimit.Fallback.Burst))
		pathLimiter = localLimiter
		go func() {
			t := time.NewTicker(1 * time.Minute)
			defer t.Stop()
			for range t.C {
				localLimiter.Cleanup()
			}
		}()
	}

	// ---- 安全中间件 ----
	corsCfg := security.DefaultCORS()
	if cfg.Server.Mode == "debug" || cfg.Server.Mode == "test" {
		corsCfg = security.PermissiveCORS()
	}
	loginProtection := security.NewLoginProtection(10, 5*time.Minute, 15*time.Minute)
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for range t.C {
			loginProtection.Cleanup()
		}
	}()

	// ---- Gin 路由 ----
	readyCheck := newReadyChecker(db, rdb, cfg.Features.MQEnabled, cfg.MQ.Type, ossClient, cfg.Server.Mode == "release")
	middlewares := []gin.HandlerFunc{
		audit.RequestID(),
		otelgin.Middleware("shortvideo"),
		metrics.GinMiddleware(),
		audit.GinMiddleware(),
		security.CORSMiddleware(corsCfg),
		security.SecureHeaders(),
		security.MaxBodySize(500 << 20), // 500MB
		loginProtection.GinMiddleware(),
		pathLimiter.GinMiddleware(ratelimit.KeyFromJWTOrUserID(cfg.JWT.Secret)),
	}
	r := api.NewRouterWithOptions(s, cfg.Storage.UploadDir, cfg.JWT.Secret,
		api.RouterOptions{
			JWTTTL:       cfg.JWT.TTL,
			AllowXUserID: cfg.Server.Mode != "release",
			ReadyCheck:   readyCheck,
			Middlewares:  middlewares,
		},
		likeSvc, feedSvc, recSvc, fanoutPub, transcodePub)
	r.GET("/metrics", func(c *gin.Context) { metrics.Handler().ServeHTTP(c.Writer, c.Request) })

	srv := &http.Server{
		Addr:         cfg.Server.Addr,
		Handler:      r,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("短视频服务已启动(Gin),监听 %s", cfg.Server.Addr)
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

// logFlag 格式化功能开关日志。
func logFlag(enabled bool, name string) string {
	if enabled {
		return name
	}
	return ""
}

func validateProductionConfig(cfg *config.Config) error {
	if cfg.Server.Mode != "release" {
		return nil
	}
	if cfg.JWT.Secret == "" || cfg.JWT.Secret == "dev-secret-change-in-production" ||
		cfg.JWT.Secret == "change-me-in-production" || cfg.JWT.Secret == "prod-change-me" ||
		len(cfg.JWT.Secret) < 32 {
		return errors.New("release 模式必须配置长度至少 32 位的 JWT_SECRET")
	}
	if cfg.Features.Seed {
		return errors.New("release 模式禁止启用演示数据 seed")
	}
	if !cfg.MysqlEnabled() {
		return errors.New("release 模式必须配置 MYSQL_DSN，不能使用内存 Store")
	}
	if !cfg.RedisEnabled() {
		return errors.New("release 模式必须配置 REDIS_ADDR，不能使用内存点赞/Feed")
	}
	if !cfg.Features.MQEnabled {
		return errors.New("release 模式必须启用 MQ，保证异步任务可靠消费")
	}
	if cfg.MQ.Type == "chan" {
		return errors.New("release 模式禁止使用进程内 ChanBus，请配置持久化 MQ 后再启用 mq_enabled")
	}
	if !cfg.OSSEnabled() || cfg.OSS.AccessKey == "" || cfg.OSS.SecretKey == "" || cfg.OSS.Bucket == "" {
		return errors.New("release 模式必须配置 OSS，不能依赖本地静态视频存储")
	}
	return nil
}

func newReadyChecker(db *sql.DB, rdb redis.UniversalClient, mqEnabled bool, mqType string, ossClient *oss.Client, requireOSS bool) func(context.Context) error {
	return func(ctx context.Context) error {
		var errs []error
		if db != nil {
			if err := db.PingContext(ctx); err != nil {
				errs = append(errs, fmt.Errorf("mysql: %w", err))
			}
		}
		if rdb != nil {
			if err := rdb.Ping(ctx).Err(); err != nil {
				errs = append(errs, fmt.Errorf("redis: %w", err))
			}
		} else if mqEnabled && mqType == "redis_stream" {
			errs = append(errs, errors.New("mq: redis_stream requires redis"))
		}
		if requireOSS {
			if ossClient == nil {
				errs = append(errs, errors.New("oss: client not initialized"))
			} else if err := ossClient.Health(ctx); err != nil {
				errs = append(errs, fmt.Errorf("oss: %w", err))
			}
		}
		return errors.Join(errs...)
	}
}

// ---- 适配器 ----

type busProducer struct {
	bus   mq.Producer
	topic string
}

func (p *busProducer) Publish(ctx context.Context, e like.LikeEvent) error {
	data, _ := json.Marshal(e)
	return p.bus.Publish(ctx, p.topic, data)
}

type fanoutPublisher struct {
	bus       mq.Producer
	topic     string
	feedStore *feed.Store
}

func (p *fanoutPublisher) PublishFanout(authorID, videoID, tsMilli int64) {
	if tsMilli <= 0 {
		tsMilli = time.Now().UnixMilli()
	}
	if p.feedStore != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := p.feedStore.AppendToOutbox(ctx, authorID, videoID, time.UnixMilli(tsMilli)); err != nil {
			log.Printf("feed outbox append failed: authorID=%d videoID=%d err=%v", authorID, videoID, err)
			return
		}
	}
	task := feed.FanoutTask{AuthorID: authorID, VideoID: videoID, TsMilli: tsMilli}
	data, _ := json.Marshal(task)
	if err := p.bus.Publish(context.Background(), p.topic, data); err != nil {
		log.Printf("fanout publish failed: authorID=%d videoID=%d err=%v", authorID, videoID, err)
	}
}

type transcodePublisherAdapter struct{ bus mq.Producer }

func (p *transcodePublisherAdapter) PublishTranscode(videoID, authorID int64, sourcePath, filename string) {
	if !media.Available() {
		return
	}
	task := transcode.Task{VideoID: videoID, AuthorID: authorID, SourcePath: sourcePath, Filename: filename}
	data, _ := json.Marshal(task)
	if err := p.bus.Publish(context.Background(), "transcode", data); err != nil {
		log.Printf("transcode publish failed: videoID=%d err=%v", videoID, err)
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

type storeStatusUpdater struct{ s *store.Store }

func (u *storeStatusUpdater) UpdateStatus(_ context.Context, videoID int64, status model.VideoStatus) error {
	return u.s.UpdateVideoStatus(videoID, status)
}

func (u *storeStatusUpdater) UpdatePlayURLs(_ context.Context, videoID int64, coverURL string, playURLs map[string]string) error {
	playURL := ""
	if url, ok := playURLs["360p"]; ok {
		playURL = url
	} else {
		for _, url := range playURLs {
			playURL = url
			break
		}
	}
	return u.s.UpdateVideoPlaybackURLs(videoID, playURL, coverURL, playURLs)
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
