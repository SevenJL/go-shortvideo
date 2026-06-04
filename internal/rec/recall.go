package rec

import (
	"context"
)

const (
	recallHotLimit    = 30 // 热门召回量
	recallFreshLimit  = 15 // 新鲜召回量
	recallCFLimit     = 20 // 协同过滤召回量
	recallSocialLimit = 10 // 社交召回量
	pageSize          = 10 // 每页返回量
)

// RecallResult 多路召回的总结果。
type RecallResult struct {
	Hot    []ScoredVideo
	Fresh  []ScoredVideo
	CF     []ScoredVideo
	Social []ScoredVideo
}

// MultiRecall 执行多路召回。
func MultiRecall(ctx context.Context, store *Store, userID int64, socialVideos []ScoredVideo) (*RecallResult, error) {
	var result RecallResult

	// 热门召回
	hot, err := store.HotRecall(ctx, userID, recallHotLimit)
	if err != nil {
		hot = nil
	}
	result.Hot = hot

	// 新鲜召回
	fresh, err := store.FreshRecall(ctx, userID, recallFreshLimit)
	if err != nil {
		fresh = nil
	}
	result.Fresh = fresh

	// 协同过滤召回
	cf, err := store.CFRecall(ctx, userID, recallCFLimit)
	if err != nil {
		cf = nil
	}
	result.CF = cf

	// 社交召回（来自关注流）
	result.Social = socialVideos

	return &result, nil
}
