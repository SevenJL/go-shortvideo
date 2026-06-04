// Package rec 实现推荐流（For You Feed）的多路召回 + 热度排序 + 多样性重排。
package rec

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	hotKeyPrefix   = "rec:hot:"
	freshKeyPrefix = "rec:fresh:"
	cfKeyPrefix    = "rec:cf:"
	cfSimPrefix    = "rec:cf_sim:"
	seenKeyPrefix  = "rec:seen:"
	shardCount     = 4     // 热门/新鲜分片数
	seenMaxSize    = 5000  // 已看集合最大容量
	seenTTL        = 7 * 24 * time.Hour
	cfTTL          = 30 * time.Minute
	cfSimTTL       = 2 * time.Hour
)

// Store 封装推荐相关的 Redis 操作。
type Store struct {
	rdb redis.UniversalClient
}

func NewStore(rdb redis.UniversalClient) *Store {
	return &Store{rdb: rdb}
}

// ---- 热门/新鲜池维护 ----

func hotKey(shard int) string  { return hotKeyPrefix + strconv.Itoa(shard) }
func freshKey(shard int) string { return freshKeyPrefix + strconv.Itoa(shard) }

// UpdateHotScore 更新视频在热门池中的热度分数（点赞/评论时调用）。
func (s *Store) UpdateHotScore(ctx context.Context, videoID int64, likeCount, commentCount int64, createdAt int64) {
	score := HeatScore(likeCount, commentCount, createdAt)
	for i := 0; i < shardCount; i++ {
		key := hotKey(i)
		_ = s.rdb.ZAdd(ctx, key, redis.Z{Score: score, Member: videoID}).Err()
	}
}

// AddToFresh 将新发布的视频加入新鲜池。
func (s *Store) AddToFresh(ctx context.Context, videoID int64, ts time.Time) {
	for i := 0; i < shardCount; i++ {
		_ = s.rdb.ZAdd(ctx, freshKey(i), redis.Z{
			Score:  float64(ts.UnixMilli()),
			Member: videoID,
		}).Err()
	}
}

// PrunePools 清理热门/新鲜池中过旧的条目（后台定期调用）。
func (s *Store) PrunePools(ctx context.Context, maxAge time.Duration) {
	minScore := float64(time.Now().Add(-maxAge).UnixMilli())
	for i := 0; i < shardCount; i++ {
		_ = s.rdb.ZRemRangeByScore(ctx, hotKey(i), "-inf", strconv.FormatFloat(minScore, 'f', 0, 64)).Err()
		_ = s.rdb.ZRemRangeByScore(ctx, freshKey(i), "-inf", strconv.FormatFloat(minScore, 'f', 0, 64)).Err()
	}
}

// ---- 召回 ----

// HotRecall 从热门池拉取 top N 视频。
func (s *Store) HotRecall(ctx context.Context, userID int64, limit int64) ([]ScoredVideo, error) {
	shard := int(userID % shardCount)
	return s.zRevRange(ctx, hotKey(shard), limit)
}

// FreshRecall 从新鲜池拉取最新的 N 个视频。
func (s *Store) FreshRecall(ctx context.Context, userID int64, limit int64) ([]ScoredVideo, error) {
	shard := int(userID % shardCount)
	return s.zRevRange(ctx, freshKey(shard), limit)
}

// CFRecall 从用户 CF 推荐池拉取。
func (s *Store) CFRecall(ctx context.Context, userID int64, limit int64) ([]ScoredVideo, error) {
	return s.zRevRange(ctx, cfKey(userID), limit)
}

// ---- 已看管理 ----

func seenKey(userID int64) string { return seenKeyPrefix + strconv.FormatInt(userID, 10) }
func cfKey(userID int64) string   { return cfKeyPrefix + strconv.FormatInt(userID, 10) }
func cfSimKey(videoID int64) string { return cfSimPrefix + strconv.FormatInt(videoID, 10) }

// MarkSeen 将一批视频标记为用户已看。
func (s *Store) MarkSeen(ctx context.Context, userID int64, videoIDs []int64) {
	if len(videoIDs) == 0 {
		return
	}
	key := seenKey(userID)
	members := make([]interface{}, len(videoIDs))
	for i, vid := range videoIDs {
		members[i] = vid
	}
	pipe := s.rdb.Pipeline()
	pipe.SAdd(ctx, key, members...)
	pipe.Expire(ctx, key, seenTTL)
	_, _ = pipe.Exec(ctx)
}

// IsSeen 批量检查视频是否已看过。
func (s *Store) IsSeen(ctx context.Context, userID int64, videoIDs []int64) (map[int64]bool, error) {
	if len(videoIDs) == 0 {
		return map[int64]bool{}, nil
	}
	key := seenKey(userID)
	pipe := s.rdb.Pipeline()
	cmds := make([]*redis.BoolCmd, len(videoIDs))
	for i, vid := range videoIDs {
		cmds[i] = pipe.SIsMember(ctx, key, vid)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	res := make(map[int64]bool, len(videoIDs))
	for i, vid := range videoIDs {
		res[vid] = cmds[i].Val()
	}
	return res, nil
}

// ---- 协同过滤数据 ----

// UpdateCFSim 更新视频的协同过滤相似视频。
func (s *Store) UpdateCFSim(ctx context.Context, videoID int64, similarVideos []ScoredVideo) {
	key := cfSimKey(videoID)
	members := make([]redis.Z, len(similarVideos))
	for i, sv := range similarVideos {
		members[i] = redis.Z{Score: sv.Score, Member: sv.VideoID}
	}
	pipe := s.rdb.Pipeline()
	if len(members) > 0 {
		pipe.ZAdd(ctx, key, members...)
	}
	pipe.Expire(ctx, key, cfSimTTL)
	_, _ = pipe.Exec(ctx)
}

// GetCFSim 获取视频的相似视频列表。
func (s *Store) GetCFSim(ctx context.Context, videoID int64, limit int64) ([]ScoredVideo, error) {
	return s.zRevRange(ctx, cfSimKey(videoID), limit)
}

// SetCFForUser 为用户缓存 CF 推荐结果。
func (s *Store) SetCFForUser(ctx context.Context, userID int64, videos []ScoredVideo) {
	key := cfKey(userID)
	members := make([]redis.Z, len(videos))
	for i, sv := range videos {
		members[i] = redis.Z{Score: sv.Score, Member: sv.VideoID}
	}
	pipe := s.rdb.Pipeline()
	pipe.Del(ctx, key)
	if len(members) > 0 {
		pipe.ZAdd(ctx, key, members...)
	}
	pipe.Expire(ctx, key, cfTTL)
	_, _ = pipe.Exec(ctx)
}

// ---- 内部工具 ----

func (s *Store) zRevRange(ctx context.Context, key string, limit int64) ([]ScoredVideo, error) {
	zs, err := s.rdb.ZRevRangeWithScores(ctx, key, 0, limit-1).Result()
	if err != nil {
		return nil, err
	}
	out := make([]ScoredVideo, 0, len(zs))
	for _, z := range zs {
		member, _ := z.Member.(string)
		vid, _ := strconv.ParseInt(member, 10, 64)
		out = append(out, ScoredVideo{VideoID: vid, Score: z.Score})
	}
	return out, nil
}
