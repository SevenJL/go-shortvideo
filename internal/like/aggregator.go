package like

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// CounterAggregator 在内存按视频聚合点赞增量，定时批量刷入 Redis。
// 可将 Redis 写 OPS 降低一个数量级，代价是计数有约一个 interval 的延迟（可接受的最终一致）。
//
// 注意：去重门（userlike SET）必须同步执行，只有计数部分可以聚合。
// 接入方式：把 Service.Like 中的 rdb.IncrBy 替换为 aggregator.Add(vid, 1)。
type CounterAggregator struct {
	mu     sync.Mutex
	deltas map[int64]int64 // videoID -> 待刷增量（正数=点赞，负数=取消）
	rdb    redis.UniversalClient
}

func NewCounterAggregator(rdb redis.UniversalClient) *CounterAggregator {
	return &CounterAggregator{
		deltas: make(map[int64]int64),
		rdb:    rdb,
	}
}

// Add 累加视频增量（非阻塞，仅加锁写 map）。
// delta=+1 为点赞，delta=-1 为取消。
func (a *CounterAggregator) Add(videoID, delta int64) {
	a.mu.Lock()
	a.deltas[videoID] += delta
	a.mu.Unlock()
}

// Run 定时刷新，直到 ctx 取消。应在独立 goroutine 中调用。
func (a *CounterAggregator) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			// 退出前刷一次，避免丢失最后一个窗口的增量
			a.flush(context.Background())
			return
		case <-t.C:
			a.flush(ctx)
		}
	}
}

func (a *CounterAggregator) flush(ctx context.Context) {
	a.mu.Lock()
	if len(a.deltas) == 0 {
		a.mu.Unlock()
		return
	}
	// swap：把当前 map 取走，立刻释放锁，新请求继续累积到新 map
	batch := a.deltas
	a.deltas = make(map[int64]int64)
	a.mu.Unlock()

	pipe := a.rdb.Pipeline()
	for vid, delta := range batch {
		if delta == 0 {
			continue
		}
		// 同一视频固定分片（vid % shards），减少 key 数量，便于读取时汇总
		shard := int(vid % CounterShards)
		pipe.IncrBy(ctx, CountShardKey(vid, shard), delta)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("aggregator flush failed (retrying next cycle): %v", err)
		a.mu.Lock()
		for vid, delta := range batch {
			a.deltas[vid] += delta
		}
		a.mu.Unlock()
	}
}
