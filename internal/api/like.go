package api

import "net/http"

// Like 处理 POST /api/videos/{id}/like(需要 X-User-Id)
func (h *Handler) Like(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireUser(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "视频 id 非法")
		return
	}
	changed, err := h.likeSvc.Like(uid, id)
	if err != nil {
		writeErr(w, storeErrStatus(err), err.Error())
		return
	}
	cnt, _ := h.likeSvc.Count(id)
	writeOK(w, map[string]interface{}{
		"changed":    changed,
		"liked":      true,
		"like_count": cnt,
	})
}

// Unlike 处理 DELETE /api/videos/{id}/like(需要 X-User-Id)
func (h *Handler) Unlike(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireUser(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "视频 id 非法")
		return
	}
	changed, err := h.likeSvc.Unlike(uid, id)
	if err != nil {
		writeErr(w, storeErrStatus(err), err.Error())
		return
	}
	cnt, _ := h.likeSvc.Count(id)
	writeOK(w, map[string]interface{}{
		"changed":    changed,
		"liked":      false,
		"like_count": cnt,
	})
}
