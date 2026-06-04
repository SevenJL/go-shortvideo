package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"shortvideo/internal/auth"
)

type createUserReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required,min=6"`
}

func (h *Handler) CreateUser(c *gin.Context) {
	var req createUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "请求体格式错误或密码少于6位"})
		return
	}
	u, err := h.store.CreateUser(req.Username, req.Password)
	if err != nil {
		c.JSON(storeErrStatus(err), gin.H{"code": storeErrStatus(err), "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": u})
}

type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func (h *Handler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "请求体格式错误"})
		return
	}
	u, err := h.store.AuthenticateUser(req.Username, req.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "用户名或密码错误"})
		return
	}
	token, err := auth.NewJWT(h.jwtSecret).GenerateToken(u.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "令牌生成失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": gin.H{"token": token, "user": u}})
}

func (h *Handler) GetUser(c *gin.Context) {
	id, err := pathID(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "用户 id 非法"})
		return
	}
	u, err := h.store.GetUser(id)
	if err != nil {
		c.JSON(storeErrStatus(err), gin.H{"code": storeErrStatus(err), "msg": err.Error()})
		return
	}
	following, followers := h.store.FollowStats(id)
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": gin.H{
		"user": u, "following_count": following, "follower_count": followers,
	}})
}
