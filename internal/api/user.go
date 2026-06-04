package api

import "net/http"

type createUserReq struct {
	Username string `json:"username"`
}

// CreateUser 处理 POST /api/users
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体格式错误")
		return
	}
	u, err := h.store.CreateUser(req.Username)
	if err != nil {
		writeErr(w, storeErrStatus(err), err.Error())
		return
	}
	writeOK(w, u)
}

// GetUser 处理 GET /api/users/{id}
func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "用户 id 非法")
		return
	}
	u, err := h.store.GetUser(id)
	if err != nil {
		writeErr(w, storeErrStatus(err), err.Error())
		return
	}
	following, followers := h.store.FollowStats(id)
	writeOK(w, map[string]interface{}{
		"user":            u,
		"following_count": following,
		"follower_count":  followers,
	})
}
