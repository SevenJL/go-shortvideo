// Package like 实现高并发点赞系统：用户维度去重 + 分片计数器 + 异步持久化。
package like

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	userLikeKeyPrefix  = "userlike:"  // SET：用户点赞过的视频（去重门）
	likeCountKeyPrefix = "likecnt:"   // STRING：分片计数器
	CounterShards      = 16           // 计数分片数，越多写热点越分散
)

// LikeEvent 是点赞/取消点赞事件，通过 MQ 异步落库。
type LikeEvent struct {
	UserID  int64  `json:"user_id"`
	VideoID int64  `json:"video_id"`
	Action  string `json:"action"`  // "like" | "unlike"
	TsMilli int64  `json:"ts_milli"`
}

// EventProducer 将点赞事件投递到 MQ（由 cmd 层适配具体 MQ 实现）。
type EventProducer interface {
	Publish(ctx context.Context, e LikeEvent) error
}

// Service 处理点赞/取消点赞的核心链路。
type Service struct {
	rdb      redis.UniversalClient
	producer EventProducer
}

func NewService(rdb redis.UniversalClient, p EventProducer) *Service {
	return &Service{rdb: rdb, producer: p}
}

func userLikeKey(uid int64) string {
	return userLikeKeyPrefix + strconv.FormatInt(uid, 10)
}

// CountShardKey 返回视频计数分片 key（导出供 aggregator / read 使用）。
func CountShardKey(vid int64, shard int) string {
	return fmt.Sprintf("%s%d:%d", likeCountKeyPrefix, vid, shard)
}

// Like 幂等点赞。
// 去重门按「用户维度」散列：不同用户落不同 key，消解爆款热 Key。
// changed=true 表示状态确实改变（首次点赞）。
func (s *Service) Like(ctx context.Context, uid, vid int64) (changed bool, err error) {
	// ① 去重门：SADD 返回 0 = 已点赞，天然幂等
	added, err := s.rdb.SAdd(ctx, userLikeKey(uid), vid).Result()
	if err != nil {
		return false, err
	}
	if added == 0 {
		return false, nil
	}

	// ② 计数：随机落一个分片，分散写热点
	shard := rand.Intn(CounterShards)
	if err := s.rdb.IncrBy(ctx, CountShardKey(vid, shard), 1).Err(); err != nil {
		// 计数失败：回滚去重门，避免「已记录但未计数」的状态偏差
		_ = s.rdb.SRem(ctx, userLikeKey(uid), vid).Err()
		return false, err
	}

	// ③ 异步持久化（尽力而为，失败不阻塞主链路）
	_ = s.producer.Publish(ctx, LikeEvent{
		UserID: uid, VideoID: vid, Action: "like",
		TsMilli: time.Now().UnixMilli(),
	})
	return true, nil
}

// Unlike 幂等取消点赞。
func (s *Service) Unlike(ctx context.Context, uid, vid int64) (changed bool, err error) {
	removed, err := s.rdb.SRem(ctx, userLikeKey(uid), vid).Result()
	if err != nil {
		return false, err
	}
	if removed == 0 {
		return false, nil
	}

	shard := rand.Intn(CounterShards)
	if err := s.rdb.DecrBy(ctx, CountShardKey(vid, shard), 1).Err(); err != nil {
		_ = s.rdb.SAdd(ctx, userLikeKey(uid), vid).Err() // 回滚
		return false, err
	}

	_ = s.producer.Publish(ctx, LikeEvent{
		UserID: uid, VideoID: vid, Action: "unlike",
		TsMilli: time.Now().UnixMilli(),
	})
	return true, nil
}
