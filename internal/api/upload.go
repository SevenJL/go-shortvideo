package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"shortvideo/pkg/media"
)

const maxUploadSize = 500 << 20 // 500 MB

func (h *Handler) Upload(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadSize)

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "缺少 file 字段或文件过大(最大500MB)"})
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !allowedVideoExt(ext) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "仅支持 mp4/mov/webm/m4v/avi/mkv 格式"})
		return
	}
	if err := validateVideoMIME(file); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": err.Error()})
		return
	}

	if err := os.MkdirAll(h.uploadDir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "创建上传目录失败"})
		return
	}

	// 保存原始文件
	name := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
	dstPath := filepath.Join(h.uploadDir, name)
	dst, err := os.Create(dstPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "保存文件失败"})
		return
	}
	fileSize, _ := io.Copy(dst, file)
	dst.Close()

	// 提取视频信息 (ffprobe)
	info, _ := media.Probe(dstPath)
	duration := 0
	width, height := 0, 0
	if info != nil {
		duration = int(info.Duration)
		width = info.Width
		height = info.Height
	}

	// 提取封面 (ffmpeg)
	coverName := strings.TrimSuffix(name, ext) + ".jpg"
	coverPath := filepath.Join(h.uploadDir, coverName)
	coverURL := ""
	if err := media.ExtractCover(dstPath, coverPath); err == nil {
		coverURL = "/uploads/" + coverName
	}

	playURL := "/uploads/" + name

	// 自动发布视频 (带提取的元信息)
	uid, _ := requireUser(c) // 鉴权组已保证登录态
	v, err := h.store.CreateVideo(uid, strings.TrimSuffix(header.Filename, ext),
		playURL, coverURL, duration, width, height, fileSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}
	_ = h.store.UpdateVideoPlaybackURLs(v.ID, playURL, coverURL, map[string]string{"original": playURL})

	if h.fanoutPub != nil {
		h.fanoutPub.PublishFanout(uid, v.ID, v.CreatedAt)
	}
	// 投递转码任务（ffmpeg 可用时）
	if h.transcodePub != nil {
		h.transcodePub.PublishTranscode(v.ID, uid, dstPath, header.Filename)
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": gin.H{
		"video_id":  v.ID,
		"status":    v.Status,
		"play_urls": gin.H{"original": playURL},
		"play_url":  playURL,
		"cover_url": coverURL,
		"filename":  header.Filename,
		"file_size": fileSize,
		"duration":  duration,
		"width":     width,
		"height":    height,
	}})
}

func allowedVideoExt(ext string) bool {
	switch ext {
	case ".mp4", ".mov", ".webm", ".m4v", ".avi", ".mkv":
		return true
	}
	return false
}

func validateVideoMIME(file io.ReadSeeker) error {
	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return fmt.Errorf("读取文件头失败")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("读取文件失败")
	}
	mime := http.DetectContentType(buf[:n])
	if strings.HasPrefix(mime, "video/") || mime == "application/octet-stream" {
		return nil
	}
	return fmt.Errorf("文件内容不是有效视频")
}
