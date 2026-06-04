package api

import (
	"net/http"

	"shortvideo/internal/model"
)

// videoItem 是返回给前端的视频结构:在视频基础上附带"当前用户是否点赞"。
type videoItem struct {
	model.Video
	Liked bool `json:"liked"`
}

type publishReq struct {
	Title    string `json:"title"`
	PlayURL  string `json:"play_url"`
	CoverURL string `json:"cover_url"`
}

// PublishVideo 处理 POST /api/videos(需要 X-User-Id)
func (h *Handler) PublishVideo(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req publishReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体格式错误")
		return
	}
	v, err := h.store.CreateVideo(uid, req.Title, req.PlayURL, req.CoverURL)
	if err != nil {
		writeErr(w, storeErrStatus(err), err.Error())
		return
	}
	// 投递写扩散任务（fan-out on write）
	if h.fanoutPub != nil {
		h.fanoutPub.PublishFanout(uid, v.ID, v.CreatedAt)
	}
	writeOK(w, v)
}

// ListVideos 处理 GET /api/videos?max_id=&limit=(广场流)
func (h *Handler) ListVideos(w http.ResponseWriter, r *http.Request) {
	maxID := queryInt(r, "max_id", 0)
	limit := int(queryInt(r, "limit", 10))
	videos := h.store.ListVideos(maxID, limit)
	h.writeFeed(w, r, videos)
}

// GetVideo 处理 GET /api/videos/{id}
func (h *Handler) GetVideo(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "视频 id 非法")
		return
	}
	v, err := h.store.GetVideo(id)
	if err != nil {
		writeErr(w, storeErrStatus(err), err.Error())
		return
	}
	item := videoItem{Video: v}
	if uid, ok := currentUserID(r); ok {
		m, _ := h.likeSvc.BatchIsLiked(r.Context(), uid, []int64{v.ID})
		item.Liked = m[v.ID]
	}
	writeOK(w, item)
}

// ListUserVideos 处理 GET /api/users/{id}/videos
func (h *Handler) ListUserVideos(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "用户 id 非法")
		return
	}
	maxID := queryInt(r, "max_id", 0)
	limit := int(queryInt(r, "limit", 10))
	videos, err := h.store.ListUserVideos(id, maxID, limit)
	if err != nil {
		writeErr(w, storeErrStatus(err), err.Error())
		return
	}
	h.writeFeed(w, r, videos)
}

// writeFeed 把视频列表装配为信息流响应:批量补全点赞态 + 计算下一页游标。
func (h *Handler) writeFeed(w http.ResponseWriter, r *http.Request, videos []model.Video) {
	items := make([]videoItem, 0, len(videos))

	var likedMap map[int64]bool
	if uid, ok := currentUserID(r); ok {
		ids := make([]int64, len(videos))
		for i, v := range videos {
			ids[i] = v.ID
		}
		likedMap, _ = h.likeSvc.BatchIsLiked(r.Context(), uid, ids)
	}

	for _, v := range videos {
		item := videoItem{Video: v}
		if likedMap != nil {
			item.Liked = likedMap[v.ID]
		}
		items = append(items, item)
	}

	// 下一页:把本页最后一条(最小 ID)作为 max_id 传回,即可取更早的内容。
	var nextCursor int64
	if len(videos) > 0 {
		nextCursor = videos[len(videos)-1].ID
	}

	writeOK(w, map[string]interface{}{
		"items":       items,
		"next_cursor": nextCursor,
	})
}
