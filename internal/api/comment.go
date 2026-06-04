package api

import "net/http"

type addCommentReq struct {
	Content string `json:"content"`
}

// AddComment 处理 POST /api/videos/{id}/comments(需要 X-User-Id)
func (h *Handler) AddComment(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireUser(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "视频 id 非法")
		return
	}
	var req addCommentReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体格式错误")
		return
	}
	c, err := h.store.AddComment(id, uid, req.Content)
	if err != nil {
		writeErr(w, storeErrStatus(err), err.Error())
		return
	}
	writeOK(w, c)
}

// ListComments 处理 GET /api/videos/{id}/comments
func (h *Handler) ListComments(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "视频 id 非法")
		return
	}
	list, err := h.store.ListComments(id)
	if err != nil {
		writeErr(w, storeErrStatus(err), err.Error())
		return
	}
	writeOK(w, map[string]interface{}{
		"items": list,
		"total": len(list),
	})
}
