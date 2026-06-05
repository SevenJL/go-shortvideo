// Package media 提供视频文件信息提取（ffprobe）和转码（ffmpeg）能力。
// ffprobe/ffmpeg 不可用时优雅降级，不影响上传流程。
package media

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

// VideoInfo 视频元信息。
type VideoInfo struct {
	Duration float64 `json:"duration"` // 秒
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Codec    string  `json:"codec_name"`
	BitRate  int64   `json:"bit_rate"`
}

type ffprobeOutput struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Duration  string `json:"duration"`
		CodecName string `json:"codec_name"`
		BitRate   string `json:"bit_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
		BitRate  string `json:"bit_rate"`
	} `json:"format"`
}

// Probe 使用 ffprobe 提取视频信息。ffprobe 不可用时返回 nil, nil。
func Probe(filePath string) (*VideoInfo, error) {
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil, nil // ffprobe 不可用，不报错
	}
	cmd := exec.Command(ffprobePath,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		filePath,
	)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	var probe ffprobeOutput
	if err := json.Unmarshal(output, &probe); err != nil {
		return nil, fmt.Errorf("ffprobe parse: %w", err)
	}

	info := &VideoInfo{}
	// 优先从 format 取时长
	if probe.Format.Duration != "" {
		fmt.Sscanf(probe.Format.Duration, "%f", &info.Duration)
	}
	// 从视频流取宽高
	for _, s := range probe.Streams {
		if s.CodecType == "video" {
			info.Width = s.Width
			info.Height = s.Height
			info.Codec = s.CodecName
			if info.Duration == 0 && s.Duration != "" {
				fmt.Sscanf(s.Duration, "%f", &info.Duration)
			}
			break
		}
	}
	return info, nil
}

// Transcode 使用 ffmpeg 转码视频到指定分辨率。
// ffmpeg 不可用时返回错误。
func Transcode(inputPath, outputPath string, width, height int, bitrate string) error {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg not found: %w", err)
	}
	scale := fmt.Sprintf("scale=%d:%d", width, height)
	args := []string{
		"-i", inputPath,
		"-vf", scale,
		"-c:v", "libx264",
		"-b:v", bitrate,
		"-c:a", "aac",
		"-b:a", "64k",
		"-preset", "fast",
		"-movflags", "+faststart",
		"-y", // 覆盖输出
		outputPath,
	}
	cmd := exec.Command(ffmpegPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %w\n%s", err, string(output))
	}
	return nil
}

// ExtractCover 使用 ffmpeg 从视频第1秒截取封面。
func ExtractCover(inputPath, outputPath string) error {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg not found: %w", err)
	}
	args := []string{
		"-i", inputPath,
		"-ss", "00:00:01",
		"-vframes", "1",
		"-q:v", "2",
		"-y",
		outputPath,
	}
	cmd := exec.Command(ffmpegPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg cover: %w\n%s", err, string(output))
	}
	return nil
}
