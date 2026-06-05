package api

import (
	"time"

	"github.com/gin-gonic/gin"

	"shortvideo/internal/auth"
	"shortvideo/internal/feed"
	"shortvideo/internal/rec"
	"shortvideo/internal/store"
)

func NewRouter(s *store.Store, uploadDir, jwtSecret string, likeSvc LikeService, feedSvc *feed.Service, recSvc *rec.Recommender, fanoutPub FanoutPublisher) *gin.Engine {
	h := NewHandler(s, uploadDir, jwtSecret, likeSvc, feedSvc, recSvc, fanoutPub)
	jwtAuth := auth.NewJWT(jwtSecret)
	authMdw := jwtAuth.GinMiddleware()

	r := gin.New()
	r.Use(ginLogger(), gin.Recovery())

	// 公开（无需鉴权）
	r.POST("/api/users", h.CreateUser)
	r.POST("/api/login", h.Login)
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"code": 0, "msg": "ok", "data": gin.H{"status": "ok"}})
	})

	// 只读（可选鉴权）
	r.GET("/api/users/:id", h.GetUser)
	r.GET("/api/users/:id/videos", h.ListUserVideos)
	r.GET("/api/videos", h.ListVideos)
	r.GET("/api/videos/:id", h.GetVideo)
	r.GET("/api/videos/:id/status", h.VideoStatus)
	r.GET("/api/videos/:id/comments", h.ListComments)

	// 鉴权组
	auth := r.Group("/api")
	auth.Use(authMdw)
	{
		auth.POST("/videos", h.PublishVideo)
		auth.POST("/videos/:id/like", h.Like)
		auth.DELETE("/videos/:id/like", h.Unlike)
		auth.POST("/videos/:id/comments", h.AddComment)
		auth.POST("/users/:id/follow", h.Follow)
		auth.DELETE("/users/:id/follow", h.Unfollow)
		auth.GET("/feed", h.FollowingFeed)
		auth.GET("/rec", h.RecommendFeed)
		auth.POST("/upload", h.Upload)
	}

	// 静态文件
	r.Static("/uploads", uploadDir)
	r.Static("/web", "./web")
	r.GET("/", func(c *gin.Context) { c.Redirect(302, "/web/demo.html") })

	return r
}

// ginLogger 简单的访问日志（替代之前的 withLogging）。
func ginLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		gin.DefaultWriter.Write([]byte(
			"[" + time.Now().Format("2006/01/02 15:04:05") + "] " +
				c.Request.Method + " " + c.Request.URL.Path + " -> " +
				itoa(c.Writer.Status()) + " (" + time.Since(start).String() + ")\n",
		))
	}
}

func itoa(n int) string { return defaultItoa(n) }

// 避免 import fmt 的简化版 itoa。
func defaultItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
