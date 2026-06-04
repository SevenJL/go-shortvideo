package api

import (
	"net/http"

	"shortvideo/internal/auth"
)

type createUserReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// CreateUser 处理 POST /api/users（注册，无需鉴权）
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体格式错误")
		return
	}
	u, err := h.store.CreateUser(req.Username, req.Password)
	if err != nil {
		writeErr(w, storeErrStatus(err), err.Error())
		return
	}
	writeOK(w, u)
}

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login 处理 POST /api/login，验证用户名密码并返回 JWT。
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体格式错误")
		return
	}
	u, err := h.store.AuthenticateUser(req.Username, req.Password)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	token, err := auth.NewJWT(h.jwtSecret).GenerateToken(u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "令牌生成失败")
		return
	}
	writeOK(w, map[string]interface{}{
		"token": token,
		"user":  u,
	})
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
