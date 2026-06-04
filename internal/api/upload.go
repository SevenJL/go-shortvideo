package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxUploadSize = 100 << 20 // 100 MB

// Upload 处理 POST /api/upload
// multipart/form-data 表单,文件字段名为 file。成功返回 { play_url, filename, size }。
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "文件过大或表单解析失败")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "缺少 file 字段")
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !allowedVideoExt(ext) {
		writeErr(w, http.StatusBadRequest, "仅支持 mp4/mov/webm/m4v/avi/mkv 等视频格式")
		return
	}

	if err := os.MkdirAll(h.uploadDir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, "创建上传目录失败")
		return
	}

	// 用纳秒时间戳命名,避免冲突
	name := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
	dstPath := filepath.Join(h.uploadDir, name)
	dst, err := os.Create(dstPath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "保存文件失败")
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		writeErr(w, http.StatusInternalServerError, "写入文件失败")
		return
	}

	writeOK(w, map[string]interface{}{
		"play_url": "/uploads/" + name, // 可直接填入发布视频接口的 play_url
		"filename": header.Filename,
		"size":     header.Size,
	})
}

func allowedVideoExt(ext string) bool {
	switch ext {
	case ".mp4", ".mov", ".webm", ".m4v", ".avi", ".mkv":
		return true
	default:
		return false
	}
}
