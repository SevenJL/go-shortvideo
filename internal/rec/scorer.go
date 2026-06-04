package rec

import (
	"math"
	"sort"
	"time"
)

// ScoredVideo 带分数的视频条目。
type ScoredVideo struct {
	VideoID  int64
	AuthorID int64
	Score    float64
}

// HeatScore 计算 HackerNews 风格的热度分数。
// likeCount*3 + commentCount*5: 评论权重大于点赞（更深度的互动）
// +1: 保证新视频有基础分（年龄项为分母时避免除以零）
// pow(age+2, 1.5): 时间衰减, age 越大分数呈指数级下降
func HeatScore(likeCount, commentCount int64, createdAtMs int64) float64 {
	ageHours := float64(time.Now().UnixMilli()-createdAtMs) / 3600000.0
	if ageHours < 0 {
		ageHours = 0
	}
	engagement := float64(likeCount)*3 + float64(commentCount)*5 + 1
	return engagement / math.Pow(ageHours+2, 1.5)
}

// MergeAndRank 合并多路召回结果，按分数排序，取 top N。
// 各路权重: Hot=1.0, Fresh=0.7, CF=1.2, Social=0.9
func MergeAndRank(hot, fresh, cf, social []ScoredVideo, limit int) []ScoredVideo {
	const (
		wHot    = 1.0
		wFresh  = 0.7
		wCF     = 1.2
		wSocial = 0.9
	)

	seen := make(map[int64]bool, len(hot)+len(fresh)+len(cf)+len(social))
	all := make([]ScoredVideo, 0, len(seen))

	add := func(videos []ScoredVideo, weight float64) {
		for _, v := range videos {
			if seen[v.VideoID] {
				continue
			}
			seen[v.VideoID] = true
			all = append(all, ScoredVideo{
				VideoID:  v.VideoID,
				AuthorID: v.AuthorID,
				Score:    v.Score * weight,
			})
		}
	}

	add(hot, wHot)
	add(fresh, wFresh)
	add(cf, wCF)
	add(social, wSocial)

	sort.Slice(all, func(i, j int) bool { return all[i].Score > all[j].Score })

	if len(all) > limit {
		all = all[:limit]
	}
	return all
}

// DiversityRerank 多样性重排：同一作者最多出现 maxPerAuthor 次。
func DiversityRerank(videos []ScoredVideo, maxPerAuthor int) []ScoredVideo {
	if maxPerAuthor <= 0 {
		return videos
	}
	result := make([]ScoredVideo, 0, len(videos))
	authorCount := make(map[int64]int, len(videos))

	for _, v := range videos {
		if authorCount[v.AuthorID] >= maxPerAuthor {
			continue
		}
		authorCount[v.AuthorID]++
		result = append(result, v)
	}
	return result
}

// FreshInjection 确保结果中至少有 freshRatio 比例的新鲜内容（按发布时间降序）。
// 如果当前结果中新鲜内容不足，从新鲜池中替换掉低分项。
func FreshInjection(merged, fresh []ScoredVideo, freshRatio float64) []ScoredVideo {
	if len(merged) == 0 || len(fresh) == 0 {
		return merged
	}

	// 统计当前结果中的新鲜视频（score 包含 fresh weight 的）
	targetFresh := int(float64(len(merged)) * freshRatio)
	if targetFresh < 1 {
		targetFresh = 1
	}

	// 简单实现：把 fresh 中不在 merged 里的视频插入到结果前面
	freshSet := make(map[int64]bool)
	for _, v := range fresh {
		freshSet[v.VideoID] = true
	}

	currentFresh := 0
	for _, v := range merged {
		if freshSet[v.VideoID] {
			currentFresh++
		}
	}

	needed := targetFresh - currentFresh
	if needed <= 0 {
		return merged
	}

	mergedSet := make(map[int64]bool)
	for _, v := range merged {
		mergedSet[v.VideoID] = true
	}

	injected := make([]ScoredVideo, 0, len(merged)+needed)
	injected = append(injected, merged...)

	for _, v := range fresh {
		if needed <= 0 {
			break
		}
		if mergedSet[v.VideoID] {
			continue
		}
		injected = append(injected, v)
		needed--
	}
	return injected
}
