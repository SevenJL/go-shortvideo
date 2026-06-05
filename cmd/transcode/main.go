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
	"shortvideo/internal/transcode"
	"shortvideo/pkg/media"
	"shortvideo/pkg/mq"
	"shortvideo/pkg/oss"
)

func main() {
	outputDir := flag.String("output-dir", "./data/uploads", "转码输出目录")
	ossEndpoint := flag.String("oss-endpoint", envOrDefault("OSS_ENDPOINT", ""), "OSS Endpoint")
	ossKey := flag.String("oss-key", envOrDefault("OSS_KEY", ""), "OSS AccessKey")
	ossSecret := flag.String("oss-secret", envOrDefault("OSS_SECRET", ""), "OSS Secret")
	ossBucket := flag.String("oss-bucket", envOrDefault("OSS_BUCKET", "shortvideo"), "OSS Bucket")
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
		LocalDir: *outputDir,
	})
	if err != nil {
		log.Fatalf("OSS 初始化失败: %v", err)
	}
	if ossClient.IsLocal() {
		log.Println("OSS 未配置，使用本地存储")
	} else {
		log.Printf("OSS 已配置: %s/%s", *ossEndpoint, *ossBucket)
	}

	// 状态更新适配器（通过 Redis 发布状态变更事件，主服务订阅并更新 store）
	updater := &redisStatusUpdater{}

	worker := transcode.NewWorker(*outputDir, ossClient, updater)

	// MQ 消费
	bus := mq.NewChanBus(1024)
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

// redisStatusUpdater 通过日志输出状态更新（生产环境可改为 Redis Pub/Sub 或 gRPC）。
type redisStatusUpdater struct{}

func (u *redisStatusUpdater) UpdateStatus(ctx context.Context, videoID int64, status model.VideoStatus) error {
	log.Printf("transcode: videoID=%d status=%d", videoID, status)
	return nil
}

func (u *redisStatusUpdater) UpdatePlayURLs(ctx context.Context, videoID int64, coverURL string, playURLs map[string]string) error {
	log.Printf("transcode: videoID=%d cover=%s playURLs=%v", videoID, coverURL, playURLs)
	return nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
