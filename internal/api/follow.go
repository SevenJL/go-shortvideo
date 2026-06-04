package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *Handler) Follow(c *gin.Context) {
	uid, ok := requireUser(c)
	if !ok {
		return
	}
	target, err := pathID(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "用户 id 非法"})
		return
	}
	if err := h.store.Follow(uid, target); err != nil {
		c.JSON(storeErrStatus(err), gin.H{"code": storeErrStatus(err), "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": gin.H{"following": true}})
}

func (h *Handler) Unfollow(c *gin.Context) {
	uid, ok := requireUser(c)
	if !ok {
		return
	}
	target, err := pathID(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "用户 id 非法"})
		return
	}
	if err := h.store.Unfollow(uid, target); err != nil {
		c.JSON(storeErrStatus(err), gin.H{"code": storeErrStatus(err), "msg": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": gin.H{"following": false}})
}

func (h *Handler) FollowingFeed(c *gin.Context) {
	uid, ok := requireUser(c)
	if !ok {
		return
	}
	maxID := queryInt(c, "max_id", 0)
	limit := int(queryInt(c, "limit", 10))

	if h.feedSvc != nil {
		cursor := float64(maxID)
		page, err := h.feedSvc.GetFollowingFeed(c.Request.Context(), uid, cursor)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": err.Error()})
			return
		}
		if len(page.Videos) > limit {
			page.Videos = page.Videos[:limit]
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "ok", "data": gin.H{
			"items": page.Videos, "next_cursor": int64(page.NextCursor),
		}})
		return
	}

	videos, err := h.store.FollowingFeed(uid, maxID, limit)
	if err != nil {
		c.JSON(storeErrStatus(err), gin.H{"code": storeErrStatus(err), "msg": err.Error()})
		return
	}
	h.writeFeed(c, videos)
}
