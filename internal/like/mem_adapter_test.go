package like

import (
	"context"
	"testing"

	"shortvideo/internal/store"
)

func setupMemSvc(t *testing.T) (*MemLikeService, *store.Store) {
	t.Helper()
	s := store.New()
	// 创建测试用户和视频
	s.CreateUser("u1", "password123")
	s.CreateUser("u2", "password123")
	s.CreateVideo(1, "视频1", "/uploads/v1.mp4", "", 0, 0, 0, 0)
	s.CreateVideo(1, "视频2", "/uploads/v2.mp4", "", 0, 0, 0, 0)
	return NewMemLikeService(s), s
}

func TestMemLikeService_Like(t *testing.T) {
	svc, _ := setupMemSvc(t)

	changed, err := svc.Like(1, 1)
	if err != nil {
		t.Fatalf("like: %v", err)
	}
	if !changed {
		t.Fatal("first like should change")
	}

	// 幂等
	changed, err = svc.Like(1, 1)
	if err != nil {
		t.Fatalf("like again: %v", err)
	}
	if changed {
		t.Fatal("second like should be idempotent")
	}

	cnt, _ := svc.Count(1)
	if cnt != 1 {
		t.Fatalf("want count=1, got %d", cnt)
	}
}

func TestMemLikeService_Unlike(t *testing.T) {
	svc, _ := setupMemSvc(t)

	svc.Like(1, 1)

	changed, err := svc.Unlike(1, 1)
	if err != nil {
		t.Fatalf("unlike: %v", err)
	}
	if !changed {
		t.Fatal("first unlike should change")
	}

	// 幂等
	changed, err = svc.Unlike(1, 1)
	if err != nil {
		t.Fatalf("unlike again: %v", err)
	}
	if changed {
		t.Fatal("second unlike should be idempotent")
	}

	cnt, _ := svc.Count(1)
	if cnt != 0 {
		t.Fatalf("want count=0, got %d", cnt)
	}
}

func TestMemLikeService_Count(t *testing.T) {
	svc, _ := setupMemSvc(t)

	svc.Like(1, 1)
	svc.Like(2, 1)

	cnt, err := svc.Count(1)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if cnt != 2 {
		t.Fatalf("want 2 likes, got %d", cnt)
	}

	// 不存在的视频
	_, err = svc.Count(999)
	if err != store.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestMemLikeService_BatchIsLiked(t *testing.T) {
	svc, _ := setupMemSvc(t)

	svc.Like(1, 1) // u1 likes v1
	svc.Like(2, 1) // u2 likes v1

	ctx := context.Background()
	m, err := svc.BatchIsLiked(ctx, 1, []int64{1, 2})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if !m[1] {
		t.Fatal("u1 should have liked v1")
	}
	if m[2] {
		t.Fatal("u1 should not have liked v2")
	}

	// u2's perspective
	m, err = svc.BatchIsLiked(ctx, 2, []int64{1, 2})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if !m[1] {
		t.Fatal("u2 should have liked v1")
	}
	if m[2] {
		t.Fatal("u2 should not have liked v2")
	}
}

func TestMemLikeService_MultiVideoLike(t *testing.T) {
	svc, _ := setupMemSvc(t)

	// Like both videos
	svc.Like(1, 1)
	svc.Like(1, 2)

	m, _ := svc.BatchIsLiked(context.Background(), 1, []int64{1, 2})
	if !m[1] || !m[2] {
		t.Fatalf("u1 should have liked both videos: %v", m)
	}

	cnt1, _ := svc.Count(1)
	cnt2, _ := svc.Count(2)
	if cnt1 != 1 || cnt2 != 1 {
		t.Fatalf("counts: v1=%d v2=%d", cnt1, cnt2)
	}
}
