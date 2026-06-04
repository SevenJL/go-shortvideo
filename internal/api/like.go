package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *Handler) Like(c *gin.Context) {
	uid, ok := requireUser(c)
	if !ok {
		return
	}
	id, err := pathID(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "视频 id 非法"})
		return
	}
	changed, err := h.likeSvc.Like(uid, id)
	if err != nil {
		c.JSON(storeErrStatus(err), gin.H{"code": storeErrStatus(err), "msg": err.Error()})
		return
	}
	cnt, _ := h.likeSvc.Count(id)
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": gin.H{
		"changed": changed, "liked": true, "like_count": cnt,
	}})
}

func (h *Handler) Unlike(c *gin.Context) {
	uid, ok := requireUser(c)
	if !ok {
		return
	}
	id, err := pathID(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "视频 id 非法"})
		return
	}
	changed, err := h.likeSvc.Unlike(uid, id)
	if err != nil {
		c.JSON(storeErrStatus(err), gin.H{"code": storeErrStatus(err), "msg": err.Error()})
		return
	}
	cnt, _ := h.likeSvc.Count(id)
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": gin.H{
		"changed": changed, "liked": false, "like_count": cnt,
	}})
}
