package rec

import (
	"context"
	"sort"
)

// FilterSeen 过滤掉用户已看过的视频。
func FilterSeen(ctx context.Context, store *Store, userID int64, videos []ScoredVideo) []ScoredVideo {
	if len(videos) == 0 {
		return nil
	}
	ids := make([]int64, len(videos))
	for i, v := range videos {
		ids[i] = v.VideoID
	}
	seen, err := store.IsSeen(ctx, userID, ids)
	if err != nil {
		return videos // Redis 故障时降级：不过滤
	}
	out := make([]ScoredVideo, 0, len(videos))
	for _, v := range videos {
		if !seen[v.VideoID] {
			out = append(out, v)
		}
	}
	return out
}

// FilterSelfAuthor 过滤掉用户自己发布的视频。
func FilterSelfAuthor(videos []ScoredVideo, userID int64) []ScoredVideo {
	out := make([]ScoredVideo, 0, len(videos))
	for _, v := range videos {
		if v.AuthorID != userID {
			out = append(out, v)
		}
	}
	return out
}

// SortByScore 按分数降序排列。
func SortByScore(videos []ScoredVideo) {
	sort.Slice(videos, func(i, j int) bool {
		return videos[i].Score > videos[j].Score
	})
}
