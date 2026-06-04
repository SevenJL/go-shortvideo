package relation

import (
	"context"
	"testing"

	"shortvideo/internal/store"
)

func setupMemRepo(t *testing.T) (*MemRepo, *store.Store) {
	t.Helper()
	s := store.New()
	// 创建用户
	s.CreateUser("alice", "password123") // ID=1
	s.CreateUser("bob", "password123")   // ID=2
	s.CreateUser("carol", "password123") // ID=3
	return NewMemRepo(s), s
}

func TestMemRepo_FollowerCount(t *testing.T) {
	repo, s := setupMemRepo(t)
	s.Follow(2, 1) // bob follows alice
	s.Follow(3, 1) // carol follows alice

	ctx := context.Background()
	cnt, err := repo.FollowerCount(ctx, 1)
	if err != nil {
		t.Fatalf("follower count: %v", err)
	}
	if cnt != 2 {
		t.Fatalf("alice should have 2 followers, got %d", cnt)
	}

	cnt, err = repo.FollowerCount(ctx, 2)
	if err != nil {
		t.Fatalf("follower count: %v", err)
	}
	if cnt != 0 {
		t.Fatalf("bob should have 0 followers, got %d", cnt)
	}
}

func TestMemRepo_ListFollowers(t *testing.T) {
	repo, s := setupMemRepo(t)
	s.Follow(2, 1) // bob follows alice
	s.Follow(3, 1) // carol follows alice

	ctx := context.Background()
	ids, next, err := repo.ListFollowers(ctx, 1, 0, 10)
	if err != nil {
		t.Fatalf("list followers: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("want 2 followers, got %d", len(ids))
	}
	if next != 0 {
		t.Fatalf("want next=0, got %d", next)
	}
}

func TestMemRepo_ListFollowers_Pagination(t *testing.T) {
	repo, s := setupMemRepo(t)
	s.Follow(2, 1)
	s.Follow(3, 1)

	ctx := context.Background()
	// 第一页: limit=1
	ids, next, err := repo.ListFollowers(ctx, 1, 0, 1)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("page 1: want 1, got %d", len(ids))
	}
	if next == 0 {
		t.Fatal("page 1: next should not be 0")
	}

	// 第二页
	ids, next, err = repo.ListFollowers(ctx, 1, next, 1)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("page 2: want 1, got %d", len(ids))
	}
	if next != 0 {
		t.Fatal("page 2: next should be 0")
	}
}

func TestMemRepo_ListFollowers_NoFollowers(t *testing.T) {
	repo, _ := setupMemRepo(t)

	ctx := context.Background()
	ids, next, err := repo.ListFollowers(ctx, 1, 0, 10)
	if err != nil {
		t.Fatalf("list followers: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("want 0, got %d", len(ids))
	}
	if next != 0 {
		t.Fatalf("want next=0, got %d", next)
	}
}

func TestMemRepo_ListFollowees(t *testing.T) {
	repo, s := setupMemRepo(t)
	s.Follow(1, 2) // alice follows bob
	s.Follow(1, 3) // alice follows carol

	ctx := context.Background()
	ids, err := repo.ListFollowees(ctx, 1)
	if err != nil {
		t.Fatalf("list followees: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("want 2 followees, got %d", len(ids))
	}
}

func TestMemRepo_ListFollowees_None(t *testing.T) {
	repo, _ := setupMemRepo(t)

	ctx := context.Background()
	ids, err := repo.ListFollowees(ctx, 1)
	if err != nil {
		t.Fatalf("list followees: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("want 0, got %d", len(ids))
	}
}
