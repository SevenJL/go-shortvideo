package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"shortvideo/internal/auth"
	"shortvideo/internal/feed"
	"shortvideo/internal/like"
	"shortvideo/internal/rec"
	"shortvideo/internal/store"
)

// LikeService 抽象点赞操作，统一 Redis 版和内存版。
type LikeService interface {
	Like(uid, vid int64) (changed bool, err error)
	Unlike(uid, vid int64) (changed bool, err error)
	Count(vid int64) (int64, error)
	BatchIsLiked(ctx context.Context, uid int64, vids []int64) (map[int64]bool, error)
}

// RedisLikeService 适配 Redis 版点赞服务。
type RedisLikeService struct {
	svc *like.Service
}

func NewRedisLikeService(svc *like.Service) *RedisLikeService {
	return &RedisLikeService{svc: svc}
}

func (r *RedisLikeService) Like(uid, vid int64) (bool, error)  { return r.svc.Like(context.Background(), uid, vid) }
func (r *RedisLikeService) Unlike(uid, vid int64) (bool, error) { return r.svc.Unlike(context.Background(), uid, vid) }
func (r *RedisLikeService) Count(vid int64) (int64, error)       { return r.svc.Count(context.Background(), vid) }
func (r *RedisLikeService) BatchIsLiked(ctx context.Context, uid int64, vids []int64) (map[int64]bool, error) {
	return r.svc.BatchIsLiked(ctx, uid, vids)
}

// FanoutPublisher 发布视频时投递写扩散任务。
type FanoutPublisher interface {
	PublishFanout(authorID, videoID, tsMilli int64)
}

// TranscodePublisher 上传视频时投递转码任务。
type TranscodePublisher interface {
	PublishTranscode(videoID, authorID int64, sourcePath, filename string)
}

// Handler 持有处理请求所需的依赖。
type Handler struct {
	store       *store.Store
	uploadDir   string
	jwtSecret   string
	likeSvc     LikeService
	feedSvc     *feed.Service
	recSvc      *rec.Recommender
	fanoutPub   FanoutPublisher
	transcodePub TranscodePublisher
}

func NewHandler(s *store.Store, uploadDir, jwtSecret string, likeSvc LikeService, feedSvc *feed.Service, recSvc *rec.Recommender, fanoutPub FanoutPublisher, transcodePub TranscodePublisher) *Handler {
	return &Handler{
		store: s, uploadDir: uploadDir, jwtSecret: jwtSecret,
		likeSvc: likeSvc, feedSvc: feedSvc, recSvc: recSvc,
		fanoutPub: fanoutPub, transcodePub: transcodePub,
	}
}

// --- 辅助函数 ---

// requireUser 从 Gin context 获取鉴权后的 userID，失败自动返回 401。
func requireUser(c *gin.Context) (int64, bool) {
	uid, ok := auth.UserIDFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "缺少认证信息"})
		return 0, false
	}
	return uid, true
}

// currentUserID 尝试获取当前用户（可选鉴权场景）。
func currentUserID(c *gin.Context) (int64, bool) {
	if uid, ok := auth.UserIDFromContext(c); ok {
		return uid, true
	}
	raw := c.GetHeader("X-User-Id")
	if raw == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// pathID 从路径参数解析 int64。
func pathID(c *gin.Context, name string) (int64, error) {
	return strconv.ParseInt(c.Param(name), 10, 64)
}

// queryInt 解析查询参数，失败或缺省返回 def。
func queryInt(c *gin.Context, name string, def int64) int64 {
	raw := c.Query(name)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return def
	}
	return v
}

// storeErrStatus 存储层错误 → HTTP 状态码。
func storeErrStatus(err error) int {
	switch err {
	case store.ErrNotFound:
		return http.StatusNotFound
	case store.ErrInvalid:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
