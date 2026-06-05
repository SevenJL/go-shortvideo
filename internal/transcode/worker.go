// Package transcode 提供视频转码 Worker，消费 MQ 任务执行多分辨率转码。
package transcode

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"shortvideo/internal/model"
	"shortvideo/pkg/media"
	"shortvideo/pkg/oss"
)

// Task 转码任务（从 MQ 消费）。
type Task struct {
	VideoID     int64  `json:"video_id"`
	AuthorID    int64  `json:"author_id"`
	SourcePath  string `json:"source_path"`  // 原始文件本地路径
	Filename    string `json:"filename"`      // 原始文件名
	TsMilli     int64  `json:"ts_milli"`
}

// StatusUpdater 更新视频状态的抽象（由主服务注入适配器）。
type StatusUpdater interface {
	UpdateStatus(ctx context.Context, videoID int64, status model.VideoStatus) error
	UpdatePlayURLs(ctx context.Context, videoID int64, coverURL string, playURLs map[string]string) error
}

// Worker 转码 Worker。
type Worker struct {
	outputDir string
	oss       *oss.Client
	updater   StatusUpdater
}

func NewWorker(outputDir string, ossClient *oss.Client, updater StatusUpdater) *Worker {
	return &Worker{outputDir: outputDir, oss: ossClient, updater: updater}
}

// Handle 处理单个转码任务。
func (w *Worker) Handle(ctx context.Context, task Task) error {
	start := time.Now()
	log.Printf("transcode: 开始 videoID=%d source=%s", task.VideoID, task.SourcePath)

	// 检查源文件
	if _, err := os.Stat(task.SourcePath); os.IsNotExist(err) {
		return fmt.Errorf("source file not found: %s", task.SourcePath)
	}

	// 1. 更新状态: 转码中
	if w.updater != nil {
		_ = w.updater.UpdateStatus(ctx, task.VideoID, model.VideoTranscoding)
	}

	// 2. 提取视频信息
	info := media.ProbeMust(task.SourcePath)
	if info == nil {
		info = &media.VideoInfo{Width: 1920, Height: 1080}
	}

	// 3. 创建输出目录
	videoDir := filepath.Join(w.outputDir, fmt.Sprintf("video_%d", task.VideoID))
	if err := os.MkdirAll(videoDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	// 4. 批量转码（智能跳过高分辨率）
	results := media.BatchTranscodeWithResize(
		task.SourcePath, videoDir, task.Filename,
		info.Width, info.Height,
		media.DefaultResolutions,
	)

	// 5. 生成封面
	coverPath := filepath.Join(videoDir, "cover.jpg")
	coverURL := ""
	if err := media.GenerateCover(task.SourcePath, coverPath); err == nil {
		coverURL = fmt.Sprintf("/uploads/video_%d/cover.jpg", task.VideoID)
	}

	// 6. 收集成功的播放地址
	playURLs := make(map[string]string)
	for _, r := range results {
		if r.Error != nil {
			log.Printf("transcode: videoID=%d %s FAILED: %v", task.VideoID, r.Resolution.Name, r.Error)
			continue
		}
		relPath := fmt.Sprintf("/uploads/video_%d/%s", task.VideoID, filepath.Base(r.OutputPath))
		playURLs[r.Resolution.Name] = relPath
		log.Printf("transcode: videoID=%d %s OK: %s", task.VideoID, r.Resolution.Name, relPath)
	}

	if len(playURLs) == 0 {
		_ = w.updater.UpdateStatus(ctx, task.VideoID, model.VideoFailed)
		return fmt.Errorf("all resolutions failed for videoID=%d", task.VideoID)
	}

	// 7. 如果有 OSS 客户端，上传到 OSS（异步，不阻塞状态更新）
	if w.oss != nil && !w.oss.IsLocal() {
		go w.uploadToOSS(videoDir, task.VideoID, playURLs, coverPath)
	}

	// 8. 更新状态: 已完成
	if w.updater != nil {
		_ = w.updater.UpdatePlayURLs(ctx, task.VideoID, coverURL, playURLs)
		_ = w.updater.UpdateStatus(ctx, task.VideoID, model.VideoReady)
	}

	log.Printf("transcode: 完成 videoID=%d resolutions=%d cover=%v elapsed=%v",
		task.VideoID, len(playURLs), coverURL != "", time.Since(start))
	return nil
}

// uploadToOSS 上传转码产物到 OSS。
func (w *Worker) uploadToOSS(videoDir string, videoID int64, playURLs map[string]string, coverPath string) {
	ossPrefix := fmt.Sprintf("videos/%d", videoID)
	// 上传各分辨率
	for res, localPath := range playURLs {
		ossKey := fmt.Sprintf("%s/%s.mp4", ossPrefix, res)
		if err := w.oss.PutObject(ossKey, filepath.Join(w.outputDir, localPath)); err != nil {
			log.Printf("transcode: OSS upload %s failed: %v", res, err)
		}
	}
	// 上传封面
	if coverPath != "" {
		ossKey := fmt.Sprintf("%s/cover.jpg", ossPrefix)
		if err := w.oss.PutObject(ossKey, coverPath); err != nil {
			log.Printf("transcode: OSS upload cover failed: %v", err)
		}
	}
}
