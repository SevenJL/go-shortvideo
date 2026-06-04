// Package relation 处理关注/粉丝关系及大 V 判定逻辑。
package relation

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	bigVFollowerThreshold = 100_000          // 粉丝数阈值，可动态调整
	bigVCacheKeyPrefix    = "bigv:cnt:"      // Redis 缓存粉丝数
	bigVCacheTTL          = 10 * time.Minute // 粉丝数变化不频繁，缓存时间适当长
)

// Repo 是关系数据的持久化抽象（MySQL 分片 / 内存适配均可实现）。
type Repo interface {
	FollowerCount(ctx context.Context, userID int64) (int64, error)
	// ListFollowers 分页拉取粉丝，cursor=0 从头开始，返回 next=0 表示末页。
	ListFollowers(ctx context.Context, authorID, cursor int64, limit int) (ids []int64, next int64, err error)
	// ListFollowees 返回 userID 关注的所有人。
	ListFollowees(ctx context.Context, userID int64) ([]int64, error)
}

// Service 提供大 V 判定和粉丝列表能力，Redis 用于缓存粉丝数。
type Service struct {
	rdb  redis.UniversalClient
	repo Repo
}

func NewService(rdb redis.UniversalClient, repo Repo) *Service {
	return &Service{rdb: rdb, repo: repo}
}

// IsBigV 判断 authorID 是否为大 V（粉丝数 ≥ 阈值）。结果有 TTL 缓存。
func (s *Service) IsBigV(ctx context.Context, authorID int64) (bool, error) {
	cnt, err := s.followerCount(ctx, authorID)
	if err != nil {
		return false, err
	}
	return cnt >= bigVFollowerThreshold, nil
}

// BigVFollowees 返回 userID 关注的人中属于大 V 的 ID 列表（供读扩散合并）。
func (s *Service) BigVFollowees(ctx context.Context, userID int64) ([]int64, error) {
	followees, err := s.repo.ListFollowees(ctx, userID)
	if err != nil {
		return nil, err
	}
	bigVs := make([]int64, 0)
	for _, fid := range followees {
		ok, err := s.IsBigV(ctx, fid)
		if err != nil {
			continue // 单个查询失败可降级跳过
		}
		if ok {
			bigVs = append(bigVs, fid)
		}
	}
	return bigVs, nil
}

// ListFollowers 分页拉取粉丝列表，供写扩散 Worker 使用。
func (s *Service) ListFollowers(ctx context.Context, authorID, cursor int64, limit int) ([]int64, int64, error) {
	return s.repo.ListFollowers(ctx, authorID, cursor, limit)
}

// followerCount 优先读 Redis 缓存，miss 时回源 DB 并回填。
func (s *Service) followerCount(ctx context.Context, userID int64) (int64, error) {
	key := bigVCacheKeyPrefix + strconv.FormatInt(userID, 10)
	val, err := s.rdb.Get(ctx, key).Int64()
	if err == nil {
		return val, nil
	}
	cnt, err := s.repo.FollowerCount(ctx, userID)
	if err != nil {
		return 0, err
	}
	// 写入缓存（尽力而为，失败不阻塞）
	_ = s.rdb.Set(ctx, key, cnt, bigVCacheTTL).Err()
	return cnt, nil
}
