package like

import (
	"context"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// Reconciler 定时对比 Redis 计数与 MySQL video_stats，偏差超过阈值时以 DB 为准修正 Redis。
type Reconciler struct {
	svc       *Service
	repo      *MysqlRepo
	rdb       redis.UniversalClient
	interval  time.Duration
	threshold int64 // 偏差阈值（绝对值），超过时才修正
}

// NewReconciler 创建对账器（使用默认阈值 5）。
func NewReconciler(svc *Service, repo *MysqlRepo, rdb redis.UniversalClient, interval time.Duration) *Reconciler {
	return NewReconcilerWithThreshold(svc, repo, rdb, interval, 5)
}

// NewReconcilerWithThreshold 创建对账器，指定阈值。
func NewReconcilerWithThreshold(svc *Service, repo *MysqlRepo, rdb redis.UniversalClient, interval time.Duration, threshold int64) *Reconciler {
	return &Reconciler{
		svc:       svc,
		repo:      repo,
		rdb:       rdb,
		interval:  interval,
		threshold: threshold,
	}
}

// Run 定时执行对账，直到 ctx 取消。
func (r *Reconciler) Run(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	log.Printf("对账器启动，间隔=%v，阈值=%d", r.interval, r.threshold)

	for {
		select {
		case <-ctx.Done():
			log.Println("对账器退出")
			return
		case <-t.C:
			r.reconcile(ctx)
		}
	}
}

func (r *Reconciler) reconcile(ctx context.Context) {
	// 获取最近有互动的活跃视频（这里简化：获取所有 video_stats 记录）
	// 生产环境应加时间范围过滤
	start := time.Now()

	// 扫描 Redis 中存在计数 key 的视频（仅扫描前 1000 个作演示）
	var cursor uint64
	var reconciled, errors int
	for {
		keys, nextCursor, err := r.rdb.Scan(ctx, cursor, likeCountKeyPrefix+"*", 100).Result()
		if err != nil {
			log.Printf("对账: scan 失败: %v", err)
			return
		}
		for _, key := range keys {
			// 从 key 解析 video_id（格式: likecnt:{vid}:{shard}）
			vid := parseVidFromKey(key)
			if vid == 0 {
				continue
			}
			redisCnt, err := r.svc.Count(ctx, vid)
			if err != nil {
				errors++
				continue
			}
			dbCnt, err := r.repo.GetLikeCount(ctx, vid)
			if err != nil {
				errors++
				continue
			}
			diff := redisCnt - dbCnt
			if abs(diff) > r.threshold {
				log.Printf("对账: video=%d Redis=%d DB=%d diff=%d 修正中...", vid, redisCnt, dbCnt, diff)
				if err := r.syncVideo(ctx, vid, dbCnt); err != nil {
					log.Printf("对账: video=%d 修正失败: %v", vid, err)
					errors++
				} else {
					reconciled++
				}
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	log.Printf("对账完成: 修正=%d 错误=%d 耗时=%v", reconciled, errors, time.Since(start))
}

// syncVideo 以 DB 计数为准，修正 Redis 分片计数。
func (r *Reconciler) syncVideo(ctx context.Context, vid, dbCnt int64) error {
	// 先读取当前 Redis 各分片值，计算总偏差
	redisCnt, err := r.svc.Count(ctx, vid)
	if err != nil {
		return err
	}
	delta := dbCnt - redisCnt
	if delta == 0 {
		return nil
	}
	// 修正第一个分片（简化，生产可均匀分配）
	shard := int(vid % CounterShards)
	return r.rdb.IncrBy(ctx, CountShardKey(vid, shard), delta).Err()
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// parseVidFromKey 从 "likecnt:123:5" 解析出 video_id=123。
func parseVidFromKey(key string) int64 {
	// key 格式: likecnt:{vid}:{shard}
	if len(key) <= len(likeCountKeyPrefix) {
		return 0
	}
	s := key[len(likeCountKeyPrefix):]
	var vid int64
	for _, c := range s {
		if c == ':' {
			break
		}
		if c >= '0' && c <= '9' {
			vid = vid*10 + int64(c-'0')
		}
	}
	return vid
}
