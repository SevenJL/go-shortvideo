package feed

import (
	"context"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// Entry 是 ZSet 中的一条记录，Score 即发布时间戳（ms）。
type Entry struct {
	VideoID int64
	Score   float64
}

// readZSetByScore 按分数倒序读取 ZSet，支持游标分页。
// cursor=0 表示从最新开始；cursor>0 时只返回 score 严格小于 cursor 的条目（开区间）。
func (s *Store) readZSetByScore(ctx context.Context, key string, cursor float64, limit int64) ([]Entry, error) {
	max := "+inf"
	if cursor > 0 {
		// "(" 前缀表示 Redis 开区间，排除游标本身避免重复
		max = "(" + strconv.FormatFloat(cursor, 'f', -1, 64)
	}
	zs, err := s.rdb.ZRevRangeByScoreWithScores(ctx, key, &redis.ZRangeBy{
		Min:   "-inf",
		Max:   max,
		Count: limit,
	}).Result()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(zs))
	for _, z := range zs {
		member, _ := z.Member.(string)
		vid, _ := strconv.ParseInt(member, 10, 64)
		out = append(out, Entry{VideoID: vid, Score: z.Score})
	}
	return out, nil
}

// ReadInbox 读取用户收件箱（推来的内容）。
func (s *Store) ReadInbox(ctx context.Context, userID int64, cursor float64, limit int64) ([]Entry, error) {
	return s.readZSetByScore(ctx, inboxKey(userID), cursor, limit)
}

// ReadOutbox 读取作者发件箱（大 V 读扩散用）。
func (s *Store) ReadOutbox(ctx context.Context, authorID int64, cursor float64, limit int64) ([]Entry, error) {
	return s.readZSetByScore(ctx, outboxKey(authorID), cursor, limit)
}
