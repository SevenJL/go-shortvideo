// Package like 提供内存版的点赞服务适配器（无 Redis 时 fallback）。
package like

import (
	"context"

	"shortvideo/internal/store"
)

// MemLikeService 将内存 Store 适配为与 Service 兼容的点赞服务。
// 无 Redis 环境可直接使用此实现。
type MemLikeService struct {
	s *store.Store
}

func NewMemLikeService(s *store.Store) *MemLikeService {
	return &MemLikeService{s: s}
}

// Like 幂等点赞，委托给内存 Store。
func (m *MemLikeService) Like(uid, vid int64) (changed bool, err error) {
	return m.s.Like(uid, vid)
}

// Unlike 幂等取消点赞。
func (m *MemLikeService) Unlike(uid, vid int64) (changed bool, err error) {
	return m.s.Unlike(uid, vid)
}

// Count 读取视频点赞数。
func (m *MemLikeService) Count(vid int64) (int64, error) {
	v, err := m.s.GetVideo(vid)
	if err != nil {
		return 0, err
	}
	return v.LikeCount, nil
}

// BatchIsLiked 批量查询当前用户对多个视频的点赞状态。ctx 参数用于兼容接口，内存版忽略。
func (m *MemLikeService) BatchIsLiked(_ context.Context, uid int64, vids []int64) (map[int64]bool, error) {
	return m.s.BatchHasLiked(uid, vids), nil
}
