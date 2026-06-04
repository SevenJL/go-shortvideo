package relation

import (
	"context"

	"shortvideo/internal/store"
)

// MemRepo 将内存 Store 适配为 relation.Repo，用于开发和测试。
// 生产环境替换为 MySQL 分片实现。
type MemRepo struct {
	s *store.Store
}

func NewMemRepo(s *store.Store) *MemRepo { return &MemRepo{s: s} }

func (r *MemRepo) FollowerCount(_ context.Context, userID int64) (int64, error) {
	_, cnt := r.s.FollowStats(userID)
	return int64(cnt), nil
}

func (r *MemRepo) ListFollowers(_ context.Context, authorID, cursor int64, limit int) ([]int64, int64, error) {
	return r.s.ListFollowers(authorID, cursor, limit)
}

func (r *MemRepo) ListFollowees(_ context.Context, userID int64) ([]int64, error) {
	return r.s.ListFollowees(userID)
}
