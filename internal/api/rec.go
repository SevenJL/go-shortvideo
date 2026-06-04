package api

import "net/http"

// RecommendFeed 处理 GET /api/rec（推荐流，需要鉴权）。
// cursor 参数是上一页最后一条的 Score（浮点数），0 表示首页。
func (h *Handler) RecommendFeed(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireUser(w, r)
	if !ok {
		return
	}
	if h.recSvc == nil {
		// 推荐服务未初始化时，fallback 到广场流
		videos := h.store.ListVideos(0, 10)
		h.writeFeed(w, r, videos)
		return
	}

	cursor := float64(queryInt(r, "cursor", 0))
	page, err := h.recSvc.GetRecommendFeed(r.Context(), uid, cursor)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, map[string]interface{}{
		"items":       page.Videos,
		"next_cursor": page.NextCursor,
	})
}
