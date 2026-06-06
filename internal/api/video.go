package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"shortvideo/internal/model"
)

type videoItem struct {
	model.Video
	Liked bool `json:"liked"`
}

type publishReq struct {
	Title    string `json:"title" binding:"required"`
	PlayURL  string `json:"play_url" binding:"required"`
	CoverURL string `json:"cover_url"`
	Duration int    `json:"duration"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int64  `json:"file_size"`
}

func (h *Handler) PublishVideo(c *gin.Context) {
	uid, ok := requireUser(c)
	if !ok {
		return
	}
	var req publishReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "请求体格式错误"})
		return
	}
	v, err := h.store.CreateVideo(uid, req.Title, req.PlayURL, req.CoverURL,
		req.Duration, req.Width, req.Height, req.FileSize)
	if err != nil {
		c.JSON(storeErrStatus(err), gin.H{"code": storeErrStatus(err), "msg": err.Error()})
		return
	}
	if h.fanoutPub != nil {
		h.fanoutPub.PublishFanout(uid, v.ID, v.CreatedAt)
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": v})
}

// VideoStatus 查询转码状态。
func (h *Handler) VideoStatus(c *gin.Context) {
	id, err := pathID(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "视频 id 非法"})
		return
	}
	v, err := h.store.GetVideo(id)
	if err != nil {
		c.JSON(storeErrStatus(err), gin.H{"code": storeErrStatus(err), "msg": err.Error()})
		return
	}
	statusText := map[model.VideoStatus]string{
		model.VideoUploading:   "上传中",
		model.VideoTranscoding: "转码中",
		model.VideoReady:       "已完成",
		model.VideoFailed:      "失败",
	}[v.Status]
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": gin.H{
		"video_id":    v.ID,
		"status":      v.Status,
		"status_text": statusText,
		"play_url":    v.PlayURL,
		"play_urls":   v.PlayURLs,
		"cover_url":   v.CoverURL,
		"duration":    v.Duration,
		"width":       v.Width,
		"height":      v.Height,
	}})
}

func (h *Handler) ListVideos(c *gin.Context) {
	maxID := queryInt(c, "max_id", 0)
	limit := int(queryInt(c, "limit", 10))
	videos := h.store.ListVideos(maxID, limit)
	h.writeFeed(c, videos)
}

func (h *Handler) GetVideo(c *gin.Context) {
	id, err := pathID(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "视频 id 非法"})
		return
	}
	v, err := h.store.GetVideo(id)
	if err != nil {
		c.JSON(storeErrStatus(err), gin.H{"code": storeErrStatus(err), "msg": err.Error()})
		return
	}
	item := videoItem{Video: v}
	if cnt, err := h.likeSvc.Count(v.ID); err == nil {
		item.LikeCount = cnt
	}
	if uid, ok := currentUserID(c); ok {
		m, _ := h.likeSvc.BatchIsLiked(c.Request.Context(), uid, []int64{v.ID})
		item.Liked = m[v.ID]
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": item})
}

func (h *Handler) ListUserVideos(c *gin.Context) {
	id, err := pathID(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "用户 id 非法"})
		return
	}
	maxID := queryInt(c, "max_id", 0)
	limit := int(queryInt(c, "limit", 10))
	videos, err := h.store.ListUserVideos(id, maxID, limit)
	if err != nil {
		c.JSON(storeErrStatus(err), gin.H{"code": storeErrStatus(err), "msg": err.Error()})
		return
	}
	h.writeFeed(c, videos)
}

func (h *Handler) writeFeed(c *gin.Context, videos []model.Video) {
	items := make([]videoItem, 0, len(videos))
	var likedMap map[int64]bool
	if uid, ok := currentUserID(c); ok {
		ids := make([]int64, len(videos))
		for i, v := range videos {
			ids[i] = v.ID
		}
		likedMap, _ = h.likeSvc.BatchIsLiked(c.Request.Context(), uid, ids)
	}
	for _, v := range videos {
		if cnt, err := h.likeSvc.Count(v.ID); err == nil {
			v.LikeCount = cnt
		}
		item := videoItem{Video: v}
		if likedMap != nil {
			item.Liked = likedMap[v.ID]
		}
		items = append(items, item)
	}
	var nextCursor int64
	if len(videos) > 0 {
		nextCursor = videos[len(videos)-1].ID
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": gin.H{
		"items": items, "next_cursor": nextCursor,
	}})
}
