package rec

import (
	"context"
	"log"
	"time"
)

// VideoVO 是推荐流返回的视频视图。
type VideoVO struct {
	VideoID      int64  `json:"video_id"`
	AuthorID     int64  `json:"author_id"`
	Title        string `json:"title"`
	PlayURL      string `json:"play_url"`
	CoverURL     string `json:"cover_url"`
	CreatedAt    int64  `json:"created_at"`
	LikeCount    int64  `json:"like_count"`
	CommentCount int64  `json:"comment_count"`
	Liked        bool   `json:"liked"`
}

// FeedPage 是推荐流分页响应。
type FeedPage struct {
	Videos     []VideoVO `json:"items"`
	NextCursor float64   `json:"next_cursor"` // 下一页的 score 游标，0 表示无更多
}

// VideoProvider 获取视频详情（由主服务注入适配器）。
type VideoProvider interface {
	BatchGet(ctx context.Context, ids []int64) ([]VideoVO, error)
}

// LikeStateProvider 查询用户点赞状态。
type LikeStateProvider interface {
	BatchIsLiked(ctx context.Context, userID int64, videoIDs []int64) (map[int64]bool, error)
}

// Recommender 推荐服务编排器。
type Recommender struct {
	store   *Store
	video   VideoProvider
	like    LikeStateProvider
}

func NewRecommender(store *Store, video VideoProvider, like LikeStateProvider) *Recommender {
	return &Recommender{store: store, video: video, like: like}
}

// GetRecommendFeed 推荐流主入口。
// cursor 是上一页最后一条的 Score（分页游标），0 表示首页。
func (r *Recommender) GetRecommendFeed(ctx context.Context, userID int64, cursor float64) (*FeedPage, error) {
	start := time.Now()
	limit := pageSize * 5 // 多取，留够过滤/重排的空间

	// 1. 多路召回
	result, err := MultiRecall(ctx, r.store, userID, nil)
	if err != nil {
		return nil, err
	}

	// 2. 合并排序
	merged := MergeAndRank(result.Hot, result.Fresh, result.CF, result.Social, limit)

	// 3. 过滤已看
	merged = FilterSeen(ctx, r.store, userID, merged)

	// 4. 过滤自己的视频
	merged = FilterSelfAuthor(merged, userID)

	// 5. 多样性重排（同一作者最多 2 条）
	merged = DiversityRerank(merged, 2)

	// 6. 新鲜注入（至少 20% 新鲜内容）
	merged = FreshInjection(merged, result.Fresh, 0.2)

	// 7. 截断到一页
	if len(merged) > pageSize {
		merged = merged[:pageSize]
	}

	// 8. 补全视频元信息
	ids := make([]int64, len(merged))
	for i, v := range merged {
		ids[i] = v.VideoID
	}
	videos, err := r.video.BatchGet(ctx, ids)
	if err != nil {
		return nil, err
	}

	// 9. 补全点赞状态
	likedMap, _ := r.like.BatchIsLiked(ctx, userID, ids)
	for i := range videos {
		videos[i].Liked = likedMap[videos[i].VideoID]
	}

	// 10. 标记已看
	r.store.MarkSeen(ctx, userID, ids)

	// 11. 计算下一页游标
	var next float64
	if len(merged) == pageSize {
		next = merged[len(merged)-1].Score
	}

	log.Printf("rec: 推荐流 user=%d items=%d cursor=%.2f next=%.2f 耗时=%v",
		userID, len(videos), cursor, next, time.Since(start))

	return &FeedPage{Videos: videos, NextCursor: next}, nil
}

// GetStore 暴露 Store 供外部维护热门池。
func (r *Recommender) GetStore() *Store { return r.store }

// RunBackgroundTasks 启动后台任务：热门分数刷新 + 池清理。
// interval 建议 5 分钟。
func (r *Recommender) RunBackgroundTasks(ctx context.Context, interval time.Duration, getter func() []VideoStats) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.refreshPools(ctx, getter())
			r.store.PrunePools(ctx, 7*24*time.Hour)
		}
	}
}

type VideoStats struct {
	VideoID      int64
	LikeCount    int64
	CommentCount int64
	CreatedAt    int64
}

func (r *Recommender) refreshPools(ctx context.Context, stats []VideoStats) {
	for _, vs := range stats {
		r.store.UpdateHotScore(ctx, vs.VideoID, vs.LikeCount, vs.CommentCount, vs.CreatedAt)
	}
	log.Printf("rec: 热门池刷新完成, 更新 %d 个视频", len(stats))
}
