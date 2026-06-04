package feed

import (
	"context"
	"sort"
)

const pageSize = 10

// VideoVO 是返回给客户端的视频视图对象，包含点赞状态等动态字段。
type VideoVO struct {
	VideoID   int64  `json:"video_id"`
	AuthorID  int64  `json:"author_id"`
	Title     string `json:"title"`
	PlayURL   string `json:"play_url"`
	CoverURL  string `json:"cover_url"`
	CreatedAt int64  `json:"created_at"`
	LikeCount int64  `json:"like_count"`
	Liked     bool   `json:"liked"`
}

// FeedPage 是单次关注流分页的响应结构。
type FeedPage struct {
	Videos     []VideoVO
	NextCursor float64 // 0 表示已无更多内容
}

// BigVProvider 返回某用户关注的大 V 列表（由 relation.Service 实现）。
type BigVProvider interface {
	BigVFollowees(ctx context.Context, userID int64) ([]int64, error)
}

// VideoHydrator 批量获取可见视频详情（过滤已删除 / 审核中的内容）。
type VideoHydrator interface {
	BatchGetVisible(ctx context.Context, ids []int64) ([]VideoVO, error)
}

// LikeStateProvider 批量查询当前用户对一批视频的点赞状态。
type LikeStateProvider interface {
	BatchIsLiked(ctx context.Context, userID int64, videoIDs []int64) (map[int64]bool, error)
}

// Service 实现推拉结合的关注流读取逻辑。
type Service struct {
	store    *Store
	relation BigVProvider
	video    VideoHydrator
	like     LikeStateProvider
}

func NewService(store *Store, relation BigVProvider, video VideoHydrator, like LikeStateProvider) *Service {
	return &Service{store: store, relation: relation, video: video, like: like}
}

// GetFollowingFeed 推拉结合读取关注流：
//  1. 推：读自己收件箱（普通用户写扩散推入的内容）
//  2. 拉：并发读关注的大 V 发件箱（读扩散）
//  3. 合并去重，按时间戳倒序，截取一页
//  4. 批量补全视频元信息 + 点赞状态，过滤不可见内容
func (svc *Service) GetFollowingFeed(ctx context.Context, userID int64, cursor float64) (*FeedPage, error) {
	// 多取几倍，保证合并后还能凑满一页
	fetch := int64(pageSize * 3)

	// 1) 推：读收件箱
	inbox, err := svc.store.ReadInbox(ctx, userID, cursor, fetch)
	if err != nil {
		return nil, err
	}

	// 2) 拉：读所关注大 V 的发件箱（并发，单个失败可降级）
	bigVs, err := svc.relation.BigVFollowees(ctx, userID)
	if err != nil {
		return nil, err
	}
	pulled := svc.readBigVOutboxesConcurrently(ctx, bigVs, cursor, fetch)

	// 3) 合并去重 + 倒序截断
	merged := mergeDedupe(inbox, pulled, pageSize)

	// 4) 计算下一页游标（本页最后一条的 score）
	var next float64
	if len(merged) == pageSize {
		next = merged[len(merged)-1].Score
	}

	// 5) 批量补全视频元信息（内部过滤已删除 / 审核中）
	ids := make([]int64, 0, len(merged))
	for _, e := range merged {
		ids = append(ids, e.VideoID)
	}
	videos, err := svc.video.BatchGetVisible(ctx, ids)
	if err != nil {
		return nil, err
	}

	// 6) 批量补全「当前用户是否点赞」
	likedMap, _ := svc.like.BatchIsLiked(ctx, userID, ids)
	for i := range videos {
		videos[i].Liked = likedMap[videos[i].VideoID]
	}

	return &FeedPage{Videos: videos, NextCursor: next}, nil
}

// mergeDedupe 合并两个条目列表，去重后按 score 倒序取前 limit 个。
// 推/拉可能包含同一视频（大 V 既被推又被拉），此处统一去重。
func mergeDedupe(a, b []Entry, limit int) []Entry {
	seen := make(map[int64]struct{}, len(a)+len(b))
	all := make([]Entry, 0, len(a)+len(b))
	for _, lst := range [][]Entry{a, b} {
		for _, e := range lst {
			if _, ok := seen[e.VideoID]; ok {
				continue
			}
			seen[e.VideoID] = struct{}{}
			all = append(all, e)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Score > all[j].Score })
	if len(all) > limit {
		all = all[:limit]
	}
	return all
}
