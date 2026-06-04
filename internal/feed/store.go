package feed

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	inboxKeyPrefix  = "feed:inbox:"
	outboxKeyPrefix = "feed:outbox:"
	timelineMaxSize = 2000             // 收件箱/发件箱最大保留条数，超出时截断最旧的
	inboxTTL        = 30 * 24 * time.Hour
)

// Store 封装关注流的 Redis 读写操作。
type Store struct {
	rdb redis.UniversalClient
}

func NewStore(rdb redis.UniversalClient) *Store { return &Store{rdb: rdb} }

func inboxKey(userID int64) string {
	return inboxKeyPrefix + strconv.FormatInt(userID, 10)
}

func outboxKey(authorID int64) string {
	return outboxKeyPrefix + strconv.FormatInt(authorID, 10)
}

// AppendToOutbox 把视频写入作者发件箱。所有作者（无论是否大 V）都写，
// 大 V 的读取流程会主动拉此 key；普通用户的内容由写扩散推入粉丝收件箱。
func (s *Store) AppendToOutbox(ctx context.Context, authorID, videoID int64, ts time.Time) error {
	key := outboxKey(authorID)
	pipe := s.rdb.TxPipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(ts.UnixMilli()), Member: videoID})
	// 删除分数最低（最旧）的超出部分，保持容量上限
	pipe.ZRemRangeByRank(ctx, key, 0, -(timelineMaxSize + 1))
	_, err := pipe.Exec(ctx)
	return err
}

// BatchPushToInbox 用 Pipeline 把一条视频批量写入多个粉丝的收件箱（写扩散）。
func (s *Store) BatchPushToInbox(ctx context.Context, followerIDs []int64, videoID int64, ts time.Time) error {
	pipe := s.rdb.Pipeline()
	score := float64(ts.UnixMilli())
	for _, uid := range followerIDs {
		key := inboxKey(uid)
		pipe.ZAdd(ctx, key, redis.Z{Score: score, Member: videoID})
		pipe.ZRemRangeByRank(ctx, key, 0, -(timelineMaxSize + 1))
		pipe.Expire(ctx, key, inboxTTL)
	}
	_, err := pipe.Exec(ctx)
	return err
}
