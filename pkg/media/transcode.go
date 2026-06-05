package media

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Resolution 转码目标分辨率。
type Resolution struct {
	Name   string // "360p", "720p", "1080p"
	Width  int
	Height int
	Bitrate string // "500k", "2000k", "4000k"
}

// DefaultResolutions 默认转码规格（抖音类似）。
var DefaultResolutions = []Resolution{
	{Name: "360p", Width: 640, Height: 360, Bitrate: "500k"},
	{Name: "540p", Width: 960, Height: 540, Bitrate: "1000k"},
	{Name: "720p", Width: 1280, Height: 720, Bitrate: "2000k"},
	{Name: "1080p", Width: 1920, Height: 1080, Bitrate: "4000k"},
}

// TranscodeResult 单个分辨率转码结果。
type TranscodeResult struct {
	Resolution Resolution
	OutputPath string
	Error      error
}

// Available 检查 ffmpeg 是否可用。
func Available() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// BatchTranscode 将视频转码为多个分辨率，返回成功的结果列表。
// outputDir: 输出目录，文件名为 {name}_{resolution}.mp4
// 单个分辨率失败不影响其他。
func BatchTranscode(inputPath, outputDir, baseName string, resolutions []Resolution) []TranscodeResult {
	results := make([]TranscodeResult, 0, len(resolutions))
	ext := filepath.Ext(inputPath)
	nameWithoutExt := strings.TrimSuffix(baseName, ext)

	for _, res := range resolutions {
		outputPath := filepath.Join(outputDir, fmt.Sprintf("%s_%s.mp4", nameWithoutExt, res.Name))
		err := Transcode(inputPath, outputPath, res.Width, res.Height, res.Bitrate)
		results = append(results, TranscodeResult{Resolution: res, OutputPath: outputPath, Error: err})
	}
	return results
}

// BatchTranscodeWithResize 智能转码：如果原视频分辨率低于目标，跳过该分辨率。
func BatchTranscodeWithResize(inputPath, outputDir, baseName string, sourceWidth, sourceHeight int, resolutions []Resolution) []TranscodeResult {
	results := make([]TranscodeResult, 0)
	ext := filepath.Ext(inputPath)
	nameWithoutExt := strings.TrimSuffix(baseName, ext)

	for _, res := range resolutions {
		// 跳过超过原视频分辨率的规格
		if res.Width > sourceWidth || res.Height > sourceHeight {
			continue
		}
		outputPath := filepath.Join(outputDir, fmt.Sprintf("%s_%s.mp4", nameWithoutExt, res.Name))
		err := Transcode(inputPath, outputPath, res.Width, res.Height, res.Bitrate)
		results = append(results, TranscodeResult{Resolution: res, OutputPath: outputPath, Error: err})
	}
	return results
}

// GenerateCover 生成封面图片。
func GenerateCover(inputPath, outputPath string) error {
	return ExtractCover(inputPath, outputPath)
}

// ProbeMust 提取视频信息，失败返回 nil。
func ProbeMust(filePath string) *VideoInfo {
	info, _ := Probe(filePath)
	return info
}

// GenerateHLS 生成 HLS (m3u8) 自适应码率流。
// 生产环境推荐，客户端可自动切换清晰度。
func GenerateHLS(inputPath, outputDir string, resolutions []Resolution) (string, error) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", fmt.Errorf("ffmpeg not found")
	}

	m3u8Path := filepath.Join(outputDir, "index.m3u8")

	args := []string{"-i", inputPath}
	// 为每个分辨率创建变体流
	filterComplex := ""
	for i, res := range resolutions {
		args = append(args,
			"-map", "0:v:0",
			"-map", "0:a:0",
			fmt.Sprintf("-s:%d", i), fmt.Sprintf("%dx%d", res.Width, res.Height),
			fmt.Sprintf("-b:v:%d", i), res.Bitrate,
		)
		filterComplex += fmt.Sprintf("[v%d]", i)
	}
	args = append(args,
		"-f", "hls",
		"-hls_time", "6",
		"-hls_list_size", "0",
		"-hls_segment_filename", filepath.Join(outputDir, "segment_%d.ts"),
		"-master_pl_name", "index.m3u8",
		m3u8Path,
	)

	cmd := exec.Command(ffmpegPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("hls transcode: %w\n%s", err, string(output))
	}
	return m3u8Path, nil
}
