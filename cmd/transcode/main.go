// cmd/transcode 是视频转码 Worker 入口。
// 消费 MQ 中的 TranscodeTask，调用 ffmpeg 生成多分辨率视频 + 封面。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"shortvideo/internal/model"
	"shortvideo/internal/store"
	"shortvideo/internal/transcode"
	"shortvideo/pkg/media"
	"shortvideo/pkg/mq"
	"shortvideo/pkg/mysqlx"
	"shortvideo/pkg/oss"
	"shortvideo/pkg/redisx"
)

func main() {
	outputDir := flag.String("output-dir", "./data/uploads", "转码输出目录")
	redisAddr := flag.String("redis", envOrDefault("REDIS_ADDR", "localhost:6379"), "Redis 地址")
	mysqlDSN := flag.String("mysql-dsn", envOrDefault("MYSQL_DSN", ""), "MySQL DSN")
	mqType := flag.String("mq-type", envOrDefault("MQ_TYPE", "chan"), "MQ 类型: chan|redis_stream")
	ossEndpoint := flag.String("oss-endpoint", envOrDefault("OSS_ENDPOINT", ""), "OSS Endpoint")
	ossKey := flag.String("oss-key", envOrDefault("OSS_ACCESS_KEY", envOrDefault("OSS_KEY", "")), "OSS AccessKey")
	ossSecret := flag.String("oss-secret", envOrDefault("OSS_SECRET_KEY", envOrDefault("OSS_SECRET", "")), "OSS Secret")
	ossBucket := flag.String("oss-bucket", envOrDefault("OSS_BUCKET", "shortvideo"), "OSS Bucket")
	ossCDNDomain := flag.String("oss-cdn-domain", envOrDefault("OSS_CDN_DOMAIN", ""), "OSS CDN domain")
	flag.Parse()

	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("创建输出目录失败: %v", err)
	}

	// 检查 ffmpeg
	if !media.Available() {
		log.Fatal("ffmpeg 不可用，请先安装: brew install ffmpeg")
	}
	log.Println("ffmpeg 可用，转码 Worker 启动中...")

	// OSS
	ossClient, err := oss.New(oss.Config{
		Endpoint: *ossEndpoint, AccessKeyID: *ossKey,
		AccessKeySecret: *ossSecret, BucketName: *ossBucket,
		CDNDomain: *ossCDNDomain, LocalDir: *outputDir,
	})
	if err != nil {
		log.Fatalf("OSS 初始化失败: %v", err)
	}
	if ossClient.IsLocal() {
		log.Println("OSS 未配置，使用本地存储")
	} else {
		log.Printf("OSS 已配置: %s/%s", *ossEndpoint, *ossBucket)
	}

	updater := statusUpdater(&logStatusUpdater{})
	if *mysqlDSN != "" {
		db, err := mysqlx.NewDB(mysqlx.Config{DSN: *mysqlDSN, MaxOpenConns: 10, MaxIdleConns: 2})
		if err != nil {
			log.Fatalf("MySQL 连接失败: %v", err)
		}
		defer db.Close()
		if err := mysqlx.RunMigrations(db); err != nil {
			log.Fatalf("MySQL 建表失败: %v", err)
		}
		updater = &storeStatusUpdater{s: store.NewMySQL(db)}
		log.Println("MySQL 状态回写已启用")
	}

	worker := transcode.NewWorker(*outputDir, ossClient, updater)

	// MQ 消费
	var bus mq.Consumer
	if *mqType == "redis_stream" {
		rdb := redisx.NewClient(*redisAddr)
		bus = mq.NewRedisStreamBus(rdb, "shortvideo", 3)
		log.Printf("使用 Redis Stream 消费 transcode: %s", *redisAddr)
	} else {
		bus = mq.NewChanBus(1024)
		log.Println("使用本地 ChanBus（仅开发/测试）")
	}
	bus.Subscribe("transcode", func(ctx context.Context, payload []byte) error {
		var task transcode.Task
		if err := json.Unmarshal(payload, &task); err != nil {
			log.Printf("transcode: 解析任务失败: %v", err)
			return nil
		}
		return worker.Handle(ctx, task)
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Println("转码 Worker 已启动，等待任务...")
	if err := bus.Run(ctx); err != nil {
		log.Printf("Worker 退出: %v", err)
	}
}

type statusUpdater interface {
	UpdateStatus(ctx context.Context, videoID int64, status model.VideoStatus) error
	UpdatePlayURLs(ctx context.Context, videoID int64, coverURL string, playURLs map[string]string) error
}

type logStatusUpdater struct{}

func (u *logStatusUpdater) UpdateStatus(ctx context.Context, videoID int64, status model.VideoStatus) error {
	log.Printf("transcode: videoID=%d status=%d", videoID, status)
	return nil
}

func (u *logStatusUpdater) UpdatePlayURLs(ctx context.Context, videoID int64, coverURL string, playURLs map[string]string) error {
	log.Printf("transcode: videoID=%d cover=%s playURLs=%v", videoID, coverURL, playURLs)
	return nil
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

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
