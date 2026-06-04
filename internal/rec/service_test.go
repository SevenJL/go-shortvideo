package rec

import (
	"math"
	"testing"
	"time"
)

func TestHeatScore_NewVideo(t *testing.T) {
	now := time.Now().UnixMilli()
	score := HeatScore(0, 0, now)
	// 新视频（age=0）应该有基础分: 1 / pow(2, 1.5) = 1/2.828 ≈ 0.353
	expected := 1.0 / math.Pow(2, 1.5)
	if math.Abs(score-expected) > 0.01 {
		t.Fatalf("new video score: want ~%.4f, got %.4f", expected, score)
	}
}

func TestHeatScore_OldVideo(t *testing.T) {
	// 24 小时前
	age := 24 * time.Hour
	ts := time.Now().Add(-age).UnixMilli()
	score := HeatScore(0, 0, ts)
	// age=24h: 1 / pow(26, 1.5) = 1/132.5 ≈ 0.0075
	expected := 1.0 / math.Pow(26, 1.5)
	if math.Abs(score-expected) > 0.001 {
		t.Fatalf("old video score: want ~%.4f, got %.4f", expected, score)
	}
}

func TestHeatScore_Engaged(t *testing.T) {
	now := time.Now().UnixMilli()
	// 100 likes + 20 comments → engagement = 100*3+20*5+1 = 401
	score := HeatScore(100, 20, now)
	expected := 401.0 / math.Pow(2, 1.5)
	if math.Abs(score-expected) > 0.01 {
		t.Fatalf("engaged score: want ~%.4f, got %.4f", expected, score)
	}
}

func TestHeatScore_CommentsWeightMore(t *testing.T) {
	now := time.Now().UnixMilli()
	scoreLike := HeatScore(10, 0, now)    // engagement=31
	scoreComment := HeatScore(0, 2, now)   // engagement=11
	// 10 likes (31) should beat 2 comments (11)
	if scoreLike <= scoreComment {
		t.Fatal("10 likes should score higher than 2 comments")
	}
}

func TestMergeAndRank_Dedup(t *testing.T) {
	hot := []ScoredVideo{{VideoID: 1, Score: 100}}
	fresh := []ScoredVideo{{VideoID: 1, Score: 50}} // same video

	result := MergeAndRank(hot, fresh, nil, nil, 10)
	if len(result) != 1 {
		t.Fatalf("want 1 after dedup, got %d", len(result))
	}
}

func TestMergeAndRank_Limit(t *testing.T) {
	hot := []ScoredVideo{
		{VideoID: 1, Score: 100},
		{VideoID: 2, Score: 90},
		{VideoID: 3, Score: 80},
	}
	result := MergeAndRank(hot, nil, nil, nil, 2)
	if len(result) != 2 {
		t.Fatalf("want 2, got %d", len(result))
	}
}

func TestDiversityRerank(t *testing.T) {
	videos := []ScoredVideo{
		{VideoID: 1, AuthorID: 1, Score: 100},
		{VideoID: 2, AuthorID: 1, Score: 90},
		{VideoID: 3, AuthorID: 1, Score: 80},
		{VideoID: 4, AuthorID: 2, Score: 70},
	}
	result := DiversityRerank(videos, 1)
	// 每个作者最多 1 条: 1,4 → len=2
	if len(result) != 2 {
		t.Fatalf("want 2, got %d", len(result))
	}
	if result[0].VideoID != 1 || result[1].VideoID != 4 {
		t.Fatalf("wrong order: %v", result)
	}
}

func TestDiversityRerank_Max2(t *testing.T) {
	videos := []ScoredVideo{
		{VideoID: 1, AuthorID: 1, Score: 100},
		{VideoID: 2, AuthorID: 1, Score: 90},
		{VideoID: 3, AuthorID: 1, Score: 80},
		{VideoID: 4, AuthorID: 1, Score: 70},
	}
	result := DiversityRerank(videos, 2)
	// 作者 1 最多 2 条
	if len(result) != 2 {
		t.Fatalf("want 2, got %d", len(result))
	}
}

func TestFilterSelfAuthor(t *testing.T) {
	videos := []ScoredVideo{
		{VideoID: 1, AuthorID: 1, Score: 100},
		{VideoID: 2, AuthorID: 2, Score: 90},
		{VideoID: 3, AuthorID: 1, Score: 80},
	}
	result := FilterSelfAuthor(videos, 1)
	if len(result) != 1 || result[0].VideoID != 2 {
		t.Fatalf("should only keep author 2's video: %v", result)
	}
}

func TestFreshInjection(t *testing.T) {
	merged := []ScoredVideo{
		{VideoID: 1, Score: 100},
		{VideoID: 2, Score: 90},
	}
	fresh := []ScoredVideo{{VideoID: 3, Score: 50}}
	result := FreshInjection(merged, fresh, 0.5)
	if len(result) < 2 {
		t.Fatalf("should have at least 2 videos, got %d", len(result))
	}
	// video 3 should be injected
	found := false
	for _, v := range result {
		if v.VideoID == 3 {
			found = true
		}
	}
	if !found {
		t.Fatal("fresh video 3 should be injected")
	}
}

func TestSortByScore(t *testing.T) {
	videos := []ScoredVideo{
		{VideoID: 1, Score: 50},
		{VideoID: 2, Score: 100},
		{VideoID: 3, Score: 75},
	}
	SortByScore(videos)
	if videos[0].VideoID != 2 || videos[1].VideoID != 3 || videos[2].VideoID != 1 {
		t.Fatalf("wrong sort order: %v", videos)
	}
}
