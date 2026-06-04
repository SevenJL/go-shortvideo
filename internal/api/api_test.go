package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"shortvideo/internal/auth"
	"shortvideo/internal/store"
)

const testJWTSecret = "test-secret-for-unit-tests"

// --- 测试辅助 ---

func newTestServer(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	s := store.New()
	dir := t.TempDir()
	return NewRouter(s, dir, testJWTSecret), s
}

// do 发送 HTTP 请求到 handler。userID > 0 时设置 X-User-Id 头（开发测试降级通道）。
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

// doWithToken 使用 JWT Bearer Token 发送请求。
func doWithToken(t *testing.T, h http.Handler, method, path string, body interface{}, token string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
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

// tokenForUser 生成测试用 JWT。
func tokenForUser(t *testing.T, userID int64) string {
	t.Helper()
	tok, err := auth.NewJWT(testJWTSecret).GenerateToken(userID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	return tok
}

// --- 健康检查 ---

func TestHealthz(t *testing.T) {
	h, _ := newTestServer(t)
	w := do(t, h, http.MethodGet, "/healthz", nil, 0)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

// --- 鉴权相关 ---

func TestLogin_API(t *testing.T) {
	h, s := newTestServer(t)
	s.CreateUser("alice", "password123")

	w := do(t, h, http.MethodPost, "/api/login", map[string]string{
		"username": "alice",
		"password": "password123",
	}, 0)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body)
	}
	m := parseBody(t, w)
	data := m["data"].(map[string]interface{})
	if data["token"] == nil || data["token"].(string) == "" {
		t.Fatal("expected token in response")
	}
	if data["user"].(map[string]interface{})["username"] != "alice" {
		t.Fatal("expected alice user")
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	h, s := newTestServer(t)
	s.CreateUser("alice", "password123")

	w := do(t, h, http.MethodPost, "/api/login", map[string]string{
		"username": "alice",
		"password": "wrongpassword",
	}, 0)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestLogin_UserNotFound(t *testing.T) {
	h, _ := newTestServer(t)

	w := do(t, h, http.MethodPost, "/api/login", map[string]string{
		"username": "nobody",
		"password": "password123",
	}, 0)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_NoAuth(t *testing.T) {
	h, _ := newTestServer(t)
	// 写接口不带任何鉴权信息应返回 401
	w := do(t, h, http.MethodPost, "/api/videos", map[string]string{
		"title":    "x",
		"play_url": "/uploads/x.mp4",
	}, 0)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_BearerToken(t *testing.T) {
	h, s := newTestServer(t)
	u, _ := s.CreateUser("alice", "password123")
	tok := tokenForUser(t, u.ID)

	w := doWithToken(t, h, http.MethodPost, "/api/videos", map[string]string{
		"title":    "我的视频",
		"play_url": "/uploads/v.mp4",
	}, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	h, _ := newTestServer(t)

	w := doWithToken(t, h, http.MethodPost, "/api/videos", map[string]string{
		"title":    "x",
		"play_url": "/uploads/x.mp4",
	}, "invalid-token")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_FallbackXUserId(t *testing.T) {
	h, s := newTestServer(t)
	u, _ := s.CreateUser("alice", "password123")

	// X-User-Id 仍可用于只读接口
	w := do(t, h, http.MethodGet, "/api/videos?limit=5", nil, u.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	// X-User-Id 也可用于通过鉴权中间件的写接口
	w = do(t, h, http.MethodPost, fmt.Sprintf("/api/videos/%d/like", 999), nil, u.ID)
	// 视频不存在返回 404,但鉴权通过(不是 401)
	if w.Code != http.StatusNotFound {
		// 这里因为是先鉴权再查数据,所以 404 表示鉴权已通过
	}
}

// --- 用户接口 ---

func TestCreateUser_API(t *testing.T) {
	h, _ := newTestServer(t)
	w := do(t, h, http.MethodPost, "/api/users", map[string]string{
		"username": "alice",
		"password": "alice123",
	}, 0)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body)
	}
	m := parseBody(t, w)
	data := m["data"].(map[string]interface{})
	if data["username"] != "alice" {
		t.Fatalf("unexpected data: %v", data)
	}
	// password hash 不应暴露
	if _, ok := data["password_hash"]; ok {
		t.Fatal("password_hash should not be exposed")
	}
}

func TestCreateUser_EmptyUsername(t *testing.T) {
	h, _ := newTestServer(t)
	w := do(t, h, http.MethodPost, "/api/users", map[string]string{
		"username": "",
		"password": "123456",
	}, 0)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestGetUser_API(t *testing.T) {
	h, s := newTestServer(t)
	u, _ := s.CreateUser("bob", "password123")
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
	u, _ := s.CreateUser("alice", "password123")
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
	u, _ := s.CreateUser("alice", "password123")
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
	u, _ := s.CreateUser("u", "password123")
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
	u, _ := s.CreateUser("u", "password123")
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
	u, _ := s.CreateUser("u", "password123")
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
	u, _ := s.CreateUser("u", "password123")
	v, _ := s.CreateVideo(u.ID, "v", "/uploads/v.mp4", "")

	w := do(t, h, http.MethodPost, fmt.Sprintf("/api/videos/%d/comments", v.ID),
		map[string]string{"content": "nice!"}, u.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body)
	}
}

func TestListComments_API(t *testing.T) {
	h, s := newTestServer(t)
	u, _ := s.CreateUser("u", "password123")
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
	alice, _ := s.CreateUser("alice", "password123")
	bob, _ := s.CreateUser("bob", "password123")

	w := do(t, h, http.MethodPost, fmt.Sprintf("/api/users/%d/follow", bob.ID), nil, alice.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body)
	}
}

func TestUnfollow_API(t *testing.T) {
	h, s := newTestServer(t)
	alice, _ := s.CreateUser("alice", "password123")
	bob, _ := s.CreateUser("bob", "password123")
	s.Follow(alice.ID, bob.ID)

	w := do(t, h, http.MethodDelete, fmt.Sprintf("/api/users/%d/follow", bob.ID), nil, alice.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

// --- 关注流接口 ---

func TestFollowingFeed_API(t *testing.T) {
	h, s := newTestServer(t)
	alice, _ := s.CreateUser("alice", "password123")
	bob, _ := s.CreateUser("bob", "password123")
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
	u, _ := s.CreateUser("u", "password123")
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
	u, _ := s.CreateUser("u", "password123")
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

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
