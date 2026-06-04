package api

import (
	"net/http"

	"shortvideo/internal/auth"
	"shortvideo/internal/store"
)

// NewRouter 构建并返回 HTTP 路由(基于 Go 1.22+ 的方法 + 路径模式匹配,无需第三方框架)。
func NewRouter(s *store.Store, uploadDir, jwtSecret string) http.Handler {
	h := NewHandler(s, uploadDir, jwtSecret)
	jwtAuth := auth.NewJWT(jwtSecret)
	authMdw := jwtAuth.Middleware

	mux := http.NewServeMux()

	// 无需鉴权
	mux.HandleFunc("POST /api/users", h.CreateUser)   // 注册
	mux.HandleFunc("POST /api/login", h.Login)         // 登录
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, map[string]string{"status": "ok"})
	})

	// 只读接口 — 可选鉴权(有 token 则补全 liked 态)
	mux.HandleFunc("GET /api/users/{id}", h.GetUser)
	mux.HandleFunc("GET /api/users/{id}/videos", h.ListUserVideos)
	mux.HandleFunc("GET /api/videos", h.ListVideos)
	mux.HandleFunc("GET /api/videos/{id}", h.GetVideo)
	mux.HandleFunc("GET /api/videos/{id}/comments", h.ListComments)

	// 需要鉴权的写接口
	mux.Handle("POST /api/videos", authMdw(http.HandlerFunc(h.PublishVideo)))
	mux.Handle("POST /api/videos/{id}/like", authMdw(http.HandlerFunc(h.Like)))
	mux.Handle("DELETE /api/videos/{id}/like", authMdw(http.HandlerFunc(h.Unlike)))
	mux.Handle("POST /api/videos/{id}/comments", authMdw(http.HandlerFunc(h.AddComment)))
	mux.Handle("POST /api/users/{id}/follow", authMdw(http.HandlerFunc(h.Follow)))
	mux.Handle("DELETE /api/users/{id}/follow", authMdw(http.HandlerFunc(h.Unfollow)))
	mux.Handle("GET /api/feed", authMdw(http.HandlerFunc(h.FollowingFeed)))
	mux.Handle("POST /api/upload", authMdw(http.HandlerFunc(h.Upload)))

	// 静态文件
	mux.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(uploadDir))))

	return withLogging(mux)
}
