package feed

import (
	"context"
	"time"
)

const fanoutBatchSize = 500 // 每批处理的粉丝数，控制单次 Pipeline 大小

// FanoutTask 是从 MQ 消费到的写扩散任务（普通用户发布时投递）。
type FanoutTask struct {
	AuthorID int64 `json:"author_id"`
	VideoID  int64 `json:"video_id"`
	TsMilli  int64 `json:"ts_milli"`
}

// FollowerLister 分页拉取粉丝列表的抽象，由 relation.Service 实现。
type FollowerLister interface {
	// cursor=0 从头开始，返回 next=0 表示末页。
	ListFollowers(ctx context.Context, authorID, cursor int64, limit int) (ids []int64, next int64, err error)
}

// FanoutWorker 消费写扩散任务：分页拉粉丝 → 批量 Pipeline 写收件箱。
type FanoutWorker struct {
	store    *Store
	relation FollowerLister
}

func NewFanoutWorker(store *Store, relation FollowerLister) *FanoutWorker {
	return &FanoutWorker{store: store, relation: relation}
}

// Handle 处理单个写扩散任务。ZAdd 本身幂等，MQ「至少一次」重试安全。
func (w *FanoutWorker) Handle(ctx context.Context, t FanoutTask) error {
	ts := time.UnixMilli(t.TsMilli)
	var cursor int64
	for {
		ids, next, err := w.relation.ListFollowers(ctx, t.AuthorID, cursor, fanoutBatchSize)
		if err != nil {
			return err // 交给 MQ 重试
		}
		if len(ids) > 0 {
			if err := w.store.BatchPushToInbox(ctx, ids, t.VideoID, ts); err != nil {
				return err
			}
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	return nil
}
