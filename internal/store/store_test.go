package store

import (
	"testing"
)

const testPass = "testpass"

// -------- 用户 --------

func TestCreateUser(t *testing.T) {
	s := New()
	u, err := s.CreateUser("alice", testPass)
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "alice" || u.ID <= 0 {
		t.Fatalf("unexpected user: %+v", u)
	}
	if u.PasswordHash == "" {
		t.Fatal("password hash should not be empty")
	}
}

func TestCreateUser_EmptyName(t *testing.T) {
	s := New()
	_, err := s.CreateUser("", testPass)
	if err != ErrInvalid {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

func TestCreateUser_EmptyPassword(t *testing.T) {
	s := New()
	_, err := s.CreateUser("alice", "")
	if err != ErrInvalid {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

func TestCreateUser_ShortPassword(t *testing.T) {
	s := New()
	_, err := s.CreateUser("alice", "12345")
	if err == nil {
		t.Fatal("expected error for short password")
	}
}

func TestGetUser_NotFound(t *testing.T) {
	s := New()
	_, err := s.GetUser(999)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGetUser(t *testing.T) {
	s := New()
	u, _ := s.CreateUser("bob", testPass)
	got, err := s.GetUser(u.ID)
	if err != nil || got.Username != "bob" {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestGetUserByUsername(t *testing.T) {
	s := New()
	s.CreateUser("charlie", testPass)

	got, err := s.GetUserByUsername("charlie")
	if err != nil || got.Username != "charlie" {
		t.Fatalf("got %+v err %v", got, err)
	}

	_, err = s.GetUserByUsername("nobody")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestAuthenticateUser(t *testing.T) {
	s := New()
	s.CreateUser("dave", "secret123")

	// 正确密码
	u, err := s.AuthenticateUser("dave", "secret123")
	if err != nil || u.Username != "dave" {
		t.Fatalf("auth failed: %+v err=%v", u, err)
	}
	if u.PasswordHash != "" {
		t.Fatal("returned user should have password hash cleared")
	}

	// 错误密码
	_, err = s.AuthenticateUser("dave", "wrongpass")
	if err != ErrWrongPassword {
		t.Fatalf("expected ErrWrongPassword, got %v", err)
	}

	// 用户不存在
	_, err = s.AuthenticateUser("nobody", "secret123")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// -------- 视频 --------

func TestCreateVideo(t *testing.T) {
	s := New()
	u, _ := s.CreateUser("alice", testPass)
	v, err := s.CreateVideo(u.ID, "测试视频", "/uploads/a.mp4", "")
	if err != nil {
		t.Fatal(err)
	}
	if v.AuthorID != u.ID || v.Title != "测试视频" {
		t.Fatalf("unexpected video: %+v", v)
	}
}

func TestCreateVideo_InvalidAuthor(t *testing.T) {
	s := New()
	_, err := s.CreateVideo(99, "x", "/uploads/a.mp4", "")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestCreateVideo_EmptyTitle(t *testing.T) {
	s := New()
	u, _ := s.CreateUser("alice", testPass)
	_, err := s.CreateVideo(u.ID, "", "/uploads/a.mp4", "")
	if err != ErrInvalid {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

func TestCreateVideo_EmptyPlayURL(t *testing.T) {
	s := New()
	u, _ := s.CreateUser("alice", testPass)
	_, err := s.CreateVideo(u.ID, "title", "", "")
	if err != ErrInvalid {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

func TestListVideos_CursorPagination(t *testing.T) {
	s := New()
	u, _ := s.CreateUser("alice", testPass)
	for i := 0; i < 5; i++ {
		s.CreateVideo(u.ID, "v", "/uploads/x.mp4", "")
	}
	page1 := s.ListVideos(0, 3)
	if len(page1) != 3 {
		t.Fatalf("want 3, got %d", len(page1))
	}
	cursor := page1[len(page1)-1].ID
	page2 := s.ListVideos(cursor, 3)
	if len(page2) != 2 {
		t.Fatalf("want 2, got %d", len(page2))
	}
	seen := map[int64]bool{}
	for _, v := range page1 {
		seen[v.ID] = true
	}
	for _, v := range page2 {
		if seen[v.ID] {
			t.Fatalf("duplicate video id %d across pages", v.ID)
		}
	}
}

func TestListUserVideos(t *testing.T) {
	s := New()
	alice, _ := s.CreateUser("alice", testPass)
	bob, _ := s.CreateUser("bob", testPass)
	s.CreateVideo(alice.ID, "a1", "/uploads/a1.mp4", "")
	s.CreateVideo(bob.ID, "b1", "/uploads/b1.mp4", "")
	s.CreateVideo(alice.ID, "a2", "/uploads/a2.mp4", "")

	vs, err := s.ListUserVideos(alice.ID, 0, 10)
	if err != nil || len(vs) != 2 {
		t.Fatalf("want 2, got %d err %v", len(vs), err)
	}
	for _, v := range vs {
		if v.AuthorID != alice.ID {
			t.Fatalf("wrong author: %+v", v)
		}
	}
}

// -------- 点赞 --------

func TestLike_Idempotent(t *testing.T) {
	s := New()
	u, _ := s.CreateUser("u", testPass)
	v, _ := s.CreateVideo(u.ID, "v", "/uploads/v.mp4", "")

	changed1, err := s.Like(u.ID, v.ID)
	if err != nil || !changed1 {
		t.Fatalf("first like: changed=%v err=%v", changed1, err)
	}

	changed2, err := s.Like(u.ID, v.ID)
	if err != nil || changed2 {
		t.Fatalf("second like should be idempotent: changed=%v err=%v", changed2, err)
	}

	got, _ := s.GetVideo(v.ID)
	if got.LikeCount != 1 {
		t.Fatalf("like_count want 1, got %d", got.LikeCount)
	}
}

func TestUnlike_Idempotent(t *testing.T) {
	s := New()
	u, _ := s.CreateUser("u", testPass)
	v, _ := s.CreateVideo(u.ID, "v", "/uploads/v.mp4", "")
	s.Like(u.ID, v.ID)

	changed, err := s.Unlike(u.ID, v.ID)
	if err != nil || !changed {
		t.Fatalf("unlike: changed=%v err=%v", changed, err)
	}

	changed2, _ := s.Unlike(u.ID, v.ID)
	if changed2 {
		t.Fatal("double unlike should be idempotent")
	}

	got, _ := s.GetVideo(v.ID)
	if got.LikeCount != 0 {
		t.Fatalf("like_count want 0, got %d", got.LikeCount)
	}
}

func TestHasLiked(t *testing.T) {
	s := New()
	u, _ := s.CreateUser("u", testPass)
	v, _ := s.CreateVideo(u.ID, "v", "/uploads/v.mp4", "")

	if s.HasLiked(u.ID, v.ID) {
		t.Fatal("should not be liked yet")
	}
	s.Like(u.ID, v.ID)
	if !s.HasLiked(u.ID, v.ID) {
		t.Fatal("should be liked")
	}
}

func TestBatchHasLiked(t *testing.T) {
	s := New()
	u, _ := s.CreateUser("u", testPass)
	v1, _ := s.CreateVideo(u.ID, "v1", "/uploads/v1.mp4", "")
	v2, _ := s.CreateVideo(u.ID, "v2", "/uploads/v2.mp4", "")
	s.Like(u.ID, v1.ID)

	m := s.BatchHasLiked(u.ID, []int64{v1.ID, v2.ID})
	if !m[v1.ID] {
		t.Fatal("v1 should be liked")
	}
	if m[v2.ID] {
		t.Fatal("v2 should not be liked")
	}
}

// -------- 评论 --------

func TestAddComment(t *testing.T) {
	s := New()
	u, _ := s.CreateUser("u", testPass)
	v, _ := s.CreateVideo(u.ID, "v", "/uploads/v.mp4", "")

	c, err := s.AddComment(v.ID, u.ID, "hello")
	if err != nil || c.Content != "hello" {
		t.Fatalf("add comment: %+v err=%v", c, err)
	}

	got, _ := s.GetVideo(v.ID)
	if got.CommentCount != 1 {
		t.Fatalf("comment_count want 1, got %d", got.CommentCount)
	}
}

func TestListComments_Order(t *testing.T) {
	s := New()
	u, _ := s.CreateUser("u", testPass)
	v, _ := s.CreateVideo(u.ID, "v", "/uploads/v.mp4", "")
	s.AddComment(v.ID, u.ID, "first")
	s.AddComment(v.ID, u.ID, "second")

	list, err := s.ListComments(v.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("want 2 comments, got %d err=%v", len(list), err)
	}
	if list[0].Content != "first" || list[1].Content != "second" {
		t.Fatalf("wrong order: %v %v", list[0].Content, list[1].Content)
	}
}

func TestAddComment_EmptyContent(t *testing.T) {
	s := New()
	u, _ := s.CreateUser("u", testPass)
	v, _ := s.CreateVideo(u.ID, "v", "/uploads/v.mp4", "")
	_, err := s.AddComment(v.ID, u.ID, "")
	if err != ErrInvalid {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

// -------- 关注 --------

func TestFollow_Unfollow(t *testing.T) {
	s := New()
	alice, _ := s.CreateUser("alice", testPass)
	bob, _ := s.CreateUser("bob", testPass)

	if err := s.Follow(alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	following, followers := s.FollowStats(bob.ID)
	if followers != 1 {
		t.Fatalf("bob followers want 1, got %d", followers)
	}
	following, _ = s.FollowStats(alice.ID)
	if following != 1 {
		t.Fatalf("alice following want 1, got %d", following)
	}

	if err := s.Unfollow(alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	_, followers = s.FollowStats(bob.ID)
	if followers != 0 {
		t.Fatalf("bob followers want 0, got %d", followers)
	}
}

func TestFollow_Self(t *testing.T) {
	s := New()
	u, _ := s.CreateUser("u", testPass)
	err := s.Follow(u.ID, u.ID)
	if err != ErrInvalid {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

func TestFollow_Idempotent(t *testing.T) {
	s := New()
	alice, _ := s.CreateUser("alice", testPass)
	bob, _ := s.CreateUser("bob", testPass)
	s.Follow(alice.ID, bob.ID)
	s.Follow(alice.ID, bob.ID)
	_, followers := s.FollowStats(bob.ID)
	if followers != 1 {
		t.Fatalf("want 1, got %d", followers)
	}
}

// -------- 关注流 --------

func TestFollowingFeed(t *testing.T) {
	s := New()
	alice, _ := s.CreateUser("alice", testPass)
	bob, _ := s.CreateUser("bob", testPass)
	carol, _ := s.CreateUser("carol", testPass)

	s.CreateVideo(alice.ID, "a1", "/uploads/a1.mp4", "")
	s.CreateVideo(bob.ID, "b1", "/uploads/b1.mp4", "")
	s.CreateVideo(carol.ID, "c1", "/uploads/c1.mp4", "")

	s.Follow(bob.ID, alice.ID)

	feed, err := s.FollowingFeed(bob.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed) != 1 || feed[0].AuthorID != alice.ID {
		t.Fatalf("want alice's video, got %+v", feed)
	}
}

func TestFollowingFeed_UserNotFound(t *testing.T) {
	s := New()
	_, err := s.FollowingFeed(999, 0, 10)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
