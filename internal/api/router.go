package api

import (
	"net/http"

	"shortvideo/internal/store"
)

// NewRouter 构建并返回 HTTP 路由(基于 Go 1.22+ 的方法 + 路径模式匹配,无需第三方框架)。
func NewRouter(s *store.Store, uploadDir string) http.Handler {
	h := NewHandler(s, uploadDir)
	mux := http.NewServeMux()

	// 用户
	mux.HandleFunc("POST /api/users", h.CreateUser)
	mux.HandleFunc("GET /api/users/{id}", h.GetUser)
	mux.HandleFunc("GET /api/users/{id}/videos", h.ListUserVideos)
	mux.HandleFunc("POST /api/users/{id}/follow", h.Follow)
	mux.HandleFunc("DELETE /api/users/{id}/follow", h.Unfollow)

	// 视频
	mux.HandleFunc("POST /api/videos", h.PublishVideo)
	mux.HandleFunc("GET /api/videos", h.ListVideos)
	mux.HandleFunc("GET /api/videos/{id}", h.GetVideo)
	mux.HandleFunc("POST /api/videos/{id}/like", h.Like)
	mux.HandleFunc("DELETE /api/videos/{id}/like", h.Unlike)
	mux.HandleFunc("POST /api/videos/{id}/comments", h.AddComment)
	mux.HandleFunc("GET /api/videos/{id}/comments", h.ListComments)

	// 关注流
	mux.HandleFunc("GET /api/feed", h.FollowingFeed)

	// 视频上传 + 静态文件访问
	mux.HandleFunc("POST /api/upload", h.Upload)
	mux.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(uploadDir))))

	// 健康检查
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, map[string]string{"status": "ok"})
	})

	return withLogging(mux)
}
