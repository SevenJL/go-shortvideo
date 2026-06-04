package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *Handler) RecommendFeed(c *gin.Context) {
	uid, ok := requireUser(c)
	if !ok {
		return
	}
	if h.recSvc == nil {
		videos := h.store.ListVideos(0, 10)
		h.writeFeed(c, videos)
		return
	}
	cursor := float64(queryInt(c, "cursor", 0))
	page, err := h.recSvc.GetRecommendFeed(c.Request.Context(), uid, cursor)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": gin.H{
		"items": page.Videos, "next_cursor": page.NextCursor,
	}})
}
