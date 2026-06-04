package like

import (
	"context"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// Count 读取视频点赞数（MGET 所有分片后求和）。
// 瞬时负值（并发取消边界场景）归零处理。
func (s *Service) Count(ctx context.Context, vid int64) (int64, error) {
	keys := make([]string, CounterShards)
	for i := 0; i < CounterShards; i++ {
		keys[i] = CountShardKey(vid, i)
	}
	vals, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return 0, err
	}
	var total int64
	for _, v := range vals {
		if v == nil {
			continue
		}
		n, _ := strconv.ParseInt(v.(string), 10, 64)
		total += n
	}
	if total < 0 {
		total = 0 // 容错：并发取消导致的瞬时负值
	}
	return total, nil
}

// BatchIsLiked 用 Pipeline 一次性判断当前用户对多个视频的点赞状态，
// 供关注流批量补全「是否已赞」字段使用。
func (s *Service) BatchIsLiked(ctx context.Context, uid int64, vids []int64) (map[int64]bool, error) {
	if len(vids) == 0 {
		return map[int64]bool{}, nil
	}
	pipe := s.rdb.Pipeline()
	key := userLikeKey(uid)
	cmds := make([]*redis.BoolCmd, len(vids))
	for i, vid := range vids {
		cmds[i] = pipe.SIsMember(ctx, key, vid)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	res := make(map[int64]bool, len(vids))
	for i, vid := range vids {
		res[vid] = cmds[i].Val()
	}
	return res, nil
}
