package api

import "net/http"

// Follow 处理 POST /api/users/{id}/follow,表示"当前用户关注 {id}"(需要 X-User-Id)
func (h *Handler) Follow(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireUser(w, r)
	if !ok {
		return
	}
	target, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "用户 id 非法")
		return
	}
	if err := h.store.Follow(uid, target); err != nil {
		writeErr(w, storeErrStatus(err), err.Error())
		return
	}
	writeOK(w, map[string]interface{}{"following": true})
}

// Unfollow 处理 DELETE /api/users/{id}/follow(需要 X-User-Id)
func (h *Handler) Unfollow(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireUser(w, r)
	if !ok {
		return
	}
	target, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "用户 id 非法")
		return
	}
	if err := h.store.Unfollow(uid, target); err != nil {
		writeErr(w, storeErrStatus(err), err.Error())
		return
	}
	writeOK(w, map[string]interface{}{"following": false})
}

// FollowingFeed 处理 GET /api/feed?max_id=&limit=(关注流,需要 X-User-Id)
func (h *Handler) FollowingFeed(w http.ResponseWriter, r *http.Request) {
	uid, ok := requireUser(w, r)
	if !ok {
		return
	}
	maxID := queryInt(r, "max_id", 0)
	limit := int(queryInt(r, "limit", 10))
	videos, err := h.store.FollowingFeed(uid, maxID, limit)
	if err != nil {
		writeErr(w, storeErrStatus(err), err.Error())
		return
	}
	h.writeFeed(w, r, videos)
}
