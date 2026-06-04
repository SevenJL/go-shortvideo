package feed

import (
	"context"
	"sync"
)

// readBigVOutboxesConcurrently 并发读取所有大 V 的发件箱并合并结果。
// 限制并发度避免打爆 Redis；单个大 V 失败时降级跳过（可降级设计）。
func (svc *Service) readBigVOutboxesConcurrently(
	ctx context.Context, bigVs []int64, cursor float64, limit int64,
) []Entry {
	if len(bigVs) == 0 {
		return nil
	}
	var (
		mu  sync.Mutex
		all []Entry
		wg  sync.WaitGroup
	)
	// 信号量限制最大并发数，防止 goroutine 爆炸
	sem := make(chan struct{}, 20)
	for _, vid := range bigVs {
		wg.Add(1)
		sem <- struct{}{}
		go func(authorID int64) {
			defer wg.Done()
			defer func() { <-sem }()
			entries, err := svc.store.ReadOutbox(ctx, authorID, cursor, limit)
			if err != nil {
				return // 降级：单个大 V 拉取失败不影响整体
			}
			mu.Lock()
			all = append(all, entries...)
			mu.Unlock()
		}(vid)
	}
	wg.Wait()
	return all
}
