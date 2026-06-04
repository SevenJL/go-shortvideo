package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type addCommentReq struct {
	Content string `json:"content" binding:"required"`
}

func (h *Handler) AddComment(c *gin.Context) {
	uid, ok := requireUser(c)
	if !ok {
		return
	}
	id, err := pathID(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "视频 id 非法"})
		return
	}
	var req addCommentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "请求体格式错误"})
		return
	}
	comment, err := h.store.AddComment(id, uid, req.Content)
	if err != nil {
		c.JSON(storeErrStatus(err), gin.H{"code": storeErrStatus(err), "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": comment})
}

func (h *Handler) ListComments(c *gin.Context) {
	id, err := pathID(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "视频 id 非法"})
		return
	}
	list, err := h.store.ListComments(id)
	if err != nil {
		c.JSON(storeErrStatus(err), gin.H{"code": storeErrStatus(err), "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": gin.H{
		"items": list, "total": len(list),
	}})
}
