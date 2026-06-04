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
)

const maxUploadSize = 100 << 20 // 100 MB

func (h *Handler) Upload(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadSize)

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "缺少 file 字段或文件过大"})
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !allowedVideoExt(ext) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "仅支持 mp4/mov/webm/m4v/avi/mkv 格式"})
		return
	}

	if err := os.MkdirAll(h.uploadDir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "创建上传目录失败"})
		return
	}

	name := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
	dstPath := filepath.Join(h.uploadDir, name)
	dst, err := os.Create(dstPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "保存文件失败"})
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "写入文件失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": gin.H{
		"play_url": "/uploads/" + name,
		"filename": header.Filename,
		"size":     header.Size,
	}})
}

func allowedVideoExt(ext string) bool {
	switch ext {
	case ".mp4", ".mov", ".webm", ".m4v", ".avi", ".mkv":
		return true
	}
	return false
}
