package like

import "context"

// Repo 是点赞持久化的数据库抽象（MySQL 分片实现 / 内存 mock 均可）。
type Repo interface {
	// UpsertLike 幂等写入点赞记录。
	// 主键 (user_id, video_id) 唯一保证幂等；updated_at 比较防止乱序覆盖。
	UpsertLike(ctx context.Context, uid, vid, ts int64, liked bool) error
	// ApplyCountDeltas 批量更新 video_stats 表的点赞计数快照。
	ApplyCountDeltas(ctx context.Context, deltas map[int64]int64) error
}

// Consumer 批量消费 MQ 中的点赞事件并落库。
// 持久化策略：先写明细行（like_record）再更新统计快照（video_stats）。
// 即使 MQ 重复投递，UpsertLike 的唯一键保证幂等性。
type Consumer struct {
	repo Repo
}

func NewConsumer(repo Repo) *Consumer { return &Consumer{repo: repo} }

// HandleBatch 批量处理一批点赞事件（MQ 消费时按批拉取，减少 DB round-trip）。
func (c *Consumer) HandleBatch(ctx context.Context, events []LikeEvent) error {
	deltas := make(map[int64]int64, len(events))
	for _, e := range events {
		switch e.Action {
		case "like":
			if err := c.repo.UpsertLike(ctx, e.UserID, e.VideoID, e.TsMilli, true); err != nil {
				return err
			}
			deltas[e.VideoID]++
		case "unlike":
			if err := c.repo.UpsertLike(ctx, e.UserID, e.VideoID, e.TsMilli, false); err != nil {
				return err
			}
			deltas[e.VideoID]--
		}
	}
	return c.repo.ApplyCountDeltas(ctx, deltas)
}
