package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"shortvideo/internal/store"
)

// --- 测试辅助 ---

func newTestServer(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	s := store.New()
	dir := t.TempDir()
	return NewRouter(s, dir), s
}

func do(t *testing.T, h http.Handler, method, path string, body interface{}, userID int64) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if userID > 0 {
		req.Header.Set("X-User-Id", fmt.Sprintf("%d", userID))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func parseBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&m); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return m
}

// --- 健康检查 ---

func TestHealthz(t *testing.T) {
	h, _ := newTestServer(t)
	w := do(t, h, http.MethodGet, "/healthz", nil, 0)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

// --- 用户接口 ---

func TestCreateUser_API(t *testing.T) {
	h, _ := newTestServer(t)
	w := do(t, h, http.MethodPost, "/api/users", map[string]string{"username": "alice"}, 0)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body)
	}
	m := parseBody(t, w)
	data := m["data"].(map[string]interface{})
	if data["username"] != "alice" {
		t.Fatalf("unexpected data: %v", data)
	}
}

func TestCreateUser_EmptyUsername(t *testing.T) {
	h, _ := newTestServer(t)
	w := do(t, h, http.MethodPost, "/api/users", map[string]string{"username": ""}, 0)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestGetUser_API(t *testing.T) {
	h, s := newTestServer(t)
	u, _ := s.CreateUser("bob")
	w := do(t, h, http.MethodGet, fmt.Sprintf("/api/users/%d", u.ID), nil, 0)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body)
	}
	m := parseBody(t, w)
	data := m["data"].(map[string]interface{})
	user := data["user"].(map[string]interface{})
	if user["username"] != "bob" {
		t.Fatalf("unexpected user: %v", user)
	}
}

func TestGetUser_NotFound(t *testing.T) {
	h, _ := newTestServer(t)
	w := do(t, h, http.MethodGet, "/api/users/9999", nil, 0)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// --- 视频接口 ---

func TestPublishVideo_API(t *testing.T) {
	h, s := newTestServer(t)
	u, _ := s.CreateUser("alice")
	w := do(t, h, http.MethodPost, "/api/videos", map[string]string{
		"title":    "测试视频",
		"play_url": "/uploads/a.mp4",
	}, u.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body)
	}
}

func TestPublishVideo_NoAuth(t *testing.T) {
	h, _ := newTestServer(t)
	w := do(t, h, http.MethodPost, "/api/videos", map[string]string{
		"title":    "x",
		"play_url": "/uploads/x.mp4",
	}, 0)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestListVideos_API(t *testing.T) {
	h, s := newTestServer(t)
	u, _ := s.CreateUser("alice")
	s.CreateVideo(u.ID, "v1", "/uploads/v1.mp4", "")
	s.CreateVideo(u.ID, "v2", "/uploads/v2.mp4", "")

	w := do(t, h, http.MethodGet, "/api/videos?limit=10", nil, 0)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	m := parseBody(t, w)
	data := m["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
}

func TestGetVideo_WithLiked(t *testing.T) {
	h, s := newTestServer(t)
	u, _ := s.CreateUser("u")
	v, _ := s.CreateVideo(u.ID, "v", "/uploads/v.mp4", "")
	s.Like(u.ID, v.ID)

	w := do(t, h, http.MethodGet, fmt.Sprintf("/api/videos/%d", v.ID), nil, u.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	m := parseBody(t, w)
	data := m["data"].(map[string]interface{})
	if data["liked"] != true {
		t.Fatalf("want liked=true, got %v", data["liked"])
	}
}

// --- 点赞接口 ---

func TestLike_API(t *testing.T) {
	h, s := newTestServer(t)
	u, _ := s.CreateUser("u")
	v, _ := s.CreateVideo(u.ID, "v", "/uploads/v.mp4", "")

	w := do(t, h, http.MethodPost, fmt.Sprintf("/api/videos/%d/like", v.ID), nil, u.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body)
	}
	m := parseBody(t, w)
	data := m["data"].(map[string]interface{})
	if data["changed"] != true || data["liked"] != true {
		t.Fatalf("unexpected: %v", data)
	}
}

func TestUnlike_API(t *testing.T) {
	h, s := newTestServer(t)
	u, _ := s.CreateUser("u")
	v, _ := s.CreateVideo(u.ID, "v", "/uploads/v.mp4", "")
	s.Like(u.ID, v.ID)

	w := do(t, h, http.MethodDelete, fmt.Sprintf("/api/videos/%d/like", v.ID), nil, u.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	m := parseBody(t, w)
	data := m["data"].(map[string]interface{})
	if data["liked"] != false {
		t.Fatalf("want liked=false, got %v", data["liked"])
	}
}

// --- 评论接口 ---

func TestAddComment_API(t *testing.T) {
	h, s := newTestServer(t)
	u, _ := s.CreateUser("u")
	v, _ := s.CreateVideo(u.ID, "v", "/uploads/v.mp4", "")

	w := do(t, h, http.MethodPost, fmt.Sprintf("/api/videos/%d/comments", v.ID),
		map[string]string{"content": "nice!"}, u.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body)
	}
}

func TestListComments_API(t *testing.T) {
	h, s := newTestServer(t)
	u, _ := s.CreateUser("u")
	v, _ := s.CreateVideo(u.ID, "v", "/uploads/v.mp4", "")
	s.AddComment(v.ID, u.ID, "first")
	s.AddComment(v.ID, u.ID, "second")

	w := do(t, h, http.MethodGet, fmt.Sprintf("/api/videos/%d/comments", v.ID), nil, 0)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	m := parseBody(t, w)
	data := m["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("want 2 comments, got %d", len(items))
	}
}

// --- 关注接口 ---

func TestFollow_API(t *testing.T) {
	h, s := newTestServer(t)
	alice, _ := s.CreateUser("alice")
	bob, _ := s.CreateUser("bob")

	w := do(t, h, http.MethodPost, fmt.Sprintf("/api/users/%d/follow", bob.ID), nil, alice.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body)
	}
}

func TestUnfollow_API(t *testing.T) {
	h, s := newTestServer(t)
	alice, _ := s.CreateUser("alice")
	bob, _ := s.CreateUser("bob")
	s.Follow(alice.ID, bob.ID)

	w := do(t, h, http.MethodDelete, fmt.Sprintf("/api/users/%d/follow", bob.ID), nil, alice.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

// --- 关注流接口 ---

func TestFollowingFeed_API(t *testing.T) {
	h, s := newTestServer(t)
	alice, _ := s.CreateUser("alice")
	bob, _ := s.CreateUser("bob")
	s.CreateVideo(alice.ID, "alice video", "/uploads/a.mp4", "")
	s.Follow(bob.ID, alice.ID)

	w := do(t, h, http.MethodGet, "/api/feed?limit=10", nil, bob.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body)
	}
	m := parseBody(t, w)
	data := m["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
}

func TestFollowingFeed_NoAuth(t *testing.T) {
	h, _ := newTestServer(t)
	w := do(t, h, http.MethodGet, "/api/feed", nil, 0)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

// --- 用户视频列表 ---

func TestListUserVideos_API(t *testing.T) {
	h, s := newTestServer(t)
	u, _ := s.CreateUser("u")
	s.CreateVideo(u.ID, "v1", "/uploads/v1.mp4", "")
	s.CreateVideo(u.ID, "v2", "/uploads/v2.mp4", "")

	w := do(t, h, http.MethodGet, fmt.Sprintf("/api/users/%d/videos", u.ID), nil, 0)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body)
	}
	m := parseBody(t, w)
	data := m["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("want 2, got %d", len(items))
	}
}

// --- 游标分页 ---

func TestListVideos_Pagination(t *testing.T) {
	h, s := newTestServer(t)
	u, _ := s.CreateUser("u")
	for i := 0; i < 5; i++ {
		s.CreateVideo(u.ID, fmt.Sprintf("v%d", i), "/uploads/x.mp4", "")
	}

	w1 := do(t, h, http.MethodGet, "/api/videos?limit=3", nil, 0)
	m1 := parseBody(t, w1)
	data1 := m1["data"].(map[string]interface{})
	cursor := int64(data1["next_cursor"].(float64))
	items1 := data1["items"].([]interface{})
	if len(items1) != 3 {
		t.Fatalf("page1: want 3, got %d", len(items1))
	}

	w2 := do(t, h, http.MethodGet, fmt.Sprintf("/api/videos?limit=3&max_id=%d", cursor), nil, 0)
	m2 := parseBody(t, w2)
	data2 := m2["data"].(map[string]interface{})
	items2 := data2["items"].([]interface{})
	if len(items2) != 2 {
		t.Fatalf("page2: want 2, got %d", len(items2))
	}
}

// TestMain 确保不依赖任何环境变量
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
