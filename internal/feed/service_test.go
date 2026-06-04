package feed

import (
	"testing"
)

func TestMergeDedupe_NoOverlap(t *testing.T) {
	a := []Entry{{VideoID: 1, Score: 100}, {VideoID: 2, Score: 90}}
	b := []Entry{{VideoID: 3, Score: 80}, {VideoID: 4, Score: 70}}

	result := mergeDedupe(a, b, 10)
	if len(result) != 4 {
		t.Fatalf("want 4, got %d", len(result))
	}
}

func TestMergeDedupe_Overlap(t *testing.T) {
	a := []Entry{{VideoID: 1, Score: 100}, {VideoID: 2, Score: 90}}
	b := []Entry{{VideoID: 2, Score: 95}, {VideoID: 3, Score: 80}} // video 2 重复

	result := mergeDedupe(a, b, 10)
	if len(result) != 3 {
		t.Fatalf("want 3 after dedup, got %d", len(result))
	}
	// video 2 应出现一次
	count := 0
	for _, e := range result {
		if e.VideoID == 2 {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("video 2 should appear once, got %d", count)
	}
}

func TestMergeDedupe_ScoreOrder(t *testing.T) {
	a := []Entry{{VideoID: 1, Score: 50}}
	b := []Entry{{VideoID: 2, Score: 100}, {VideoID: 3, Score: 75}}

	result := mergeDedupe(a, b, 10)
	if len(result) != 3 {
		t.Fatalf("want 3, got %d", len(result))
	}
	// 应按 score 倒序
	if result[0].VideoID != 2 || result[1].VideoID != 3 || result[2].VideoID != 1 {
		t.Fatalf("wrong order: %v", result)
	}
}

func TestMergeDedupe_Limit(t *testing.T) {
	a := []Entry{{VideoID: 1, Score: 100}, {VideoID: 2, Score: 90}}
	b := []Entry{{VideoID: 3, Score: 80}, {VideoID: 4, Score: 70}}

	result := mergeDedupe(a, b, 2)
	if len(result) != 2 {
		t.Fatalf("want 2, got %d", len(result))
	}
	if result[0].VideoID != 1 || result[1].VideoID != 2 {
		t.Fatalf("wrong items: %v", result)
	}
}

func TestMergeDedupe_Empty(t *testing.T) {
	result := mergeDedupe(nil, nil, 10)
	if len(result) != 0 {
		t.Fatalf("want 0, got %d", len(result))
	}

	result = mergeDedupe([]Entry{{VideoID: 1, Score: 100}}, nil, 10)
	if len(result) != 1 {
		t.Fatalf("want 1, got %d", len(result))
	}
}

func TestMergeDedupe_AllSameScore(t *testing.T) {
	a := []Entry{{VideoID: 1, Score: 100}}
	b := []Entry{{VideoID: 2, Score: 100}, {VideoID: 3, Score: 100}}

	result := mergeDedupe(a, b, 10)
	if len(result) != 3 {
		t.Fatalf("want 3, got %d", len(result))
	}
}

func TestMergeDedupe_AllOverlap(t *testing.T) {
	a := []Entry{{VideoID: 1, Score: 100}, {VideoID: 2, Score: 90}}
	b := []Entry{{VideoID: 1, Score: 100}, {VideoID: 2, Score: 90}}

	result := mergeDedupe(a, b, 10)
	if len(result) != 2 {
		t.Fatalf("want 2, got %d", len(result))
	}
}

func TestVideoVO_Fields(t *testing.T) {
	vo := VideoVO{
		VideoID:   1,
		AuthorID:  2,
		Title:     "test",
		PlayURL:   "/uploads/test.mp4",
		CoverURL:  "/covers/test.jpg",
		CreatedAt: 1234567890,
		LikeCount: 42,
		Liked:     true,
	}
	if vo.VideoID != 1 || vo.LikeCount != 42 || !vo.Liked {
		t.Fatalf("VideoVO fields mismatch: %+v", vo)
	}
}

func TestFeedPage_EmptyCursor(t *testing.T) {
	page := &FeedPage{Videos: nil, NextCursor: 0}
	if page.NextCursor != 0 {
		t.Fatal("empty feed should have next_cursor=0")
	}
	if len(page.Videos) != 0 {
		t.Fatal("empty feed should have no videos")
	}
}
