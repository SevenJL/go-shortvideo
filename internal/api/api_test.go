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
	"shortvideo/internal/like"
	"shortvideo/internal/store"

	"github.com/gin-gonic/gin"
)

const testJWTSecret = "test-secret-for-unit-tests"

func init() { gin.SetMode(gin.TestMode) }

func newTestServer(t *testing.T) (*gin.Engine, *store.Store) {
	t.Helper()
	s := store.New()
	dir := t.TempDir()
	likeSvc := like.NewMemLikeService(s)
	return NewRouter(s, dir, testJWTSecret, likeSvc, nil, nil, nil), s
}

func do(t *testing.T, r *gin.Engine, method, path string, body interface{}, userID int64) *httptest.ResponseRecorder {
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
	r.ServeHTTP(w, req)
	return w
}

func doWithToken(t *testing.T, r *gin.Engine, method, path string, body interface{}, token string) *httptest.ResponseRecorder {
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
	r.ServeHTTP(w, req)
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
	r, _ := newTestServer(t)
	w := do(t, r, http.MethodGet, "/healthz", nil, 0)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

// --- 鉴权 ---

func TestLogin_API(t *testing.T) {
	r, s := newTestServer(t)
	s.CreateUser("alice", "password123")

	w := do(t, r, http.MethodPost, "/api/login", map[string]string{
		"username": "alice", "password": "password123",
	}, 0)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body)
	}
	m := parseBody(t, w)
	data := m["data"].(map[string]interface{})
	if data["token"] == nil || data["token"].(string) == "" {
		t.Fatal("expected token")
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	r, s := newTestServer(t)
	s.CreateUser("alice", "password123")
	w := do(t, r, http.MethodPost, "/api/login", map[string]string{
		"username": "alice", "password": "wrong",
	}, 0)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestLogin_UserNotFound(t *testing.T) {
	r, _ := newTestServer(t)
	w := do(t, r, http.MethodPost, "/api/login", map[string]string{
		"username": "nobody", "password": "x",
	}, 0)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_NoAuth(t *testing.T) {
	r, _ := newTestServer(t)
	w := do(t, r, http.MethodPost, "/api/videos", map[string]string{
		"title": "x", "play_url": "/x.mp4",
	}, 0)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_BearerToken(t *testing.T) {
	r, s := newTestServer(t)
	u, _ := s.CreateUser("alice", "password123")
	tok := tokenForUser(t, u.ID)
	w := doWithToken(t, r, http.MethodPost, "/api/videos", map[string]string{
		"title": "my video", "play_url": "/v.mp4",
	}, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	r, _ := newTestServer(t)
	w := doWithToken(t, r, http.MethodPost, "/api/videos", map[string]string{
		"title": "x", "play_url": "/x.mp4",
	}, "invalid.token")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_FallbackXUserId(t *testing.T) {
	r, s := newTestServer(t)
	u, _ := s.CreateUser("alice", "password123")
	w := do(t, r, http.MethodGet, "/api/videos?limit=5", nil, u.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

// --- 用户 ---

func TestCreateUser_API(t *testing.T) {
	r, _ := newTestServer(t)
	w := do(t, r, http.MethodPost, "/api/users", map[string]string{
		"username": "alice", "password": "alice123",
	}, 0)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body)
	}
	m := parseBody(t, w)
	if m["data"].(map[string]interface{})["username"] != "alice" {
		t.Fatal("unexpected username")
	}
}

func TestCreateUser_EmptyUsername(t *testing.T) {
	r, _ := newTestServer(t)
	w := do(t, r, http.MethodPost, "/api/users", map[string]string{
		"username": "", "password": "123456",
	}, 0)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestGetUser_API(t *testing.T) {
	r, s := newTestServer(t)
	u, _ := s.CreateUser("bob", "password123")
	w := do(t, r, http.MethodGet, fmt.Sprintf("/api/users/%d", u.ID), nil, 0)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestGetUser_NotFound(t *testing.T) {
	r, _ := newTestServer(t)
	w := do(t, r, http.MethodGet, "/api/users/9999", nil, 0)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// --- 视频 ---

func TestPublishVideo_API(t *testing.T) {
	r, s := newTestServer(t)
	u, _ := s.CreateUser("alice", "password123")
	w := do(t, r, http.MethodPost, "/api/videos", map[string]string{
		"title": "test", "play_url": "/a.mp4",
	}, u.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body)
	}
}

func TestPublishVideo_NoAuth(t *testing.T) {
	r, _ := newTestServer(t)
	w := do(t, r, http.MethodPost, "/api/videos", map[string]string{
		"title": "x", "play_url": "/x.mp4",
	}, 0)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestListVideos_API(t *testing.T) {
	r, s := newTestServer(t)
	u, _ := s.CreateUser("alice", "password123")
	s.CreateVideo(u.ID, "v1", "/v1.mp4", "")
	s.CreateVideo(u.ID, "v2", "/v2.mp4", "")
	w := do(t, r, http.MethodGet, "/api/videos?limit=10", nil, 0)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	m := parseBody(t, w)
	items := m["data"].(map[string]interface{})["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("want 2, got %d", len(items))
	}
}

func TestGetVideo_WithLiked(t *testing.T) {
	r, s := newTestServer(t)
	u, _ := s.CreateUser("u", "password123")
	v, _ := s.CreateVideo(u.ID, "v", "/v.mp4", "")
	s.Like(u.ID, v.ID)
	w := do(t, r, http.MethodGet, fmt.Sprintf("/api/videos/%d", v.ID), nil, u.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	m := parseBody(t, w)
	if m["data"].(map[string]interface{})["liked"] != true {
		t.Fatal("want liked=true")
	}
}

// --- 点赞 ---

func TestLike_API(t *testing.T) {
	r, s := newTestServer(t)
	u, _ := s.CreateUser("u", "password123")
	v, _ := s.CreateVideo(u.ID, "v", "/v.mp4", "")
	w := do(t, r, http.MethodPost, fmt.Sprintf("/api/videos/%d/like", v.ID), nil, u.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	m := parseBody(t, w)
	d := m["data"].(map[string]interface{})
	if d["changed"] != true || d["liked"] != true {
		t.Fatal("unexpected like result")
	}
}

func TestUnlike_API(t *testing.T) {
	r, s := newTestServer(t)
	u, _ := s.CreateUser("u", "password123")
	v, _ := s.CreateVideo(u.ID, "v", "/v.mp4", "")
	s.Like(u.ID, v.ID)
	w := do(t, r, http.MethodDelete, fmt.Sprintf("/api/videos/%d/like", v.ID), nil, u.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

// --- 评论 ---

func TestAddComment_API(t *testing.T) {
	r, s := newTestServer(t)
	u, _ := s.CreateUser("u", "password123")
	v, _ := s.CreateVideo(u.ID, "v", "/v.mp4", "")
	w := do(t, r, http.MethodPost, fmt.Sprintf("/api/videos/%d/comments", v.ID),
		map[string]string{"content": "nice!"}, u.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestListComments_API(t *testing.T) {
	r, s := newTestServer(t)
	u, _ := s.CreateUser("u", "password123")
	v, _ := s.CreateVideo(u.ID, "v", "/v.mp4", "")
	s.AddComment(v.ID, u.ID, "first")
	s.AddComment(v.ID, u.ID, "second")
	w := do(t, r, http.MethodGet, fmt.Sprintf("/api/videos/%d/comments", v.ID), nil, 0)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

// --- 关注 ---

func TestFollow_API(t *testing.T) {
	r, s := newTestServer(t)
	alice, _ := s.CreateUser("alice", "password123")
	bob, _ := s.CreateUser("bob", "password123")
	w := do(t, r, http.MethodPost, fmt.Sprintf("/api/users/%d/follow", bob.ID), nil, alice.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestUnfollow_API(t *testing.T) {
	r, s := newTestServer(t)
	alice, _ := s.CreateUser("alice", "password123")
	bob, _ := s.CreateUser("bob", "password123")
	s.Follow(alice.ID, bob.ID)
	w := do(t, r, http.MethodDelete, fmt.Sprintf("/api/users/%d/follow", bob.ID), nil, alice.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestFollowingFeed_API(t *testing.T) {
	r, s := newTestServer(t)
	alice, _ := s.CreateUser("alice", "password123")
	bob, _ := s.CreateUser("bob", "password123")
	s.CreateVideo(alice.ID, "a", "/a.mp4", "")
	s.Follow(bob.ID, alice.ID)
	w := do(t, r, http.MethodGet, "/api/feed?limit=10", nil, bob.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestFollowingFeed_NoAuth(t *testing.T) {
	r, _ := newTestServer(t)
	w := do(t, r, http.MethodGet, "/api/feed", nil, 0)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestListUserVideos_API(t *testing.T) {
	r, s := newTestServer(t)
	u, _ := s.CreateUser("u", "password123")
	s.CreateVideo(u.ID, "v1", "/v1.mp4", "")
	s.CreateVideo(u.ID, "v2", "/v2.mp4", "")
	w := do(t, r, http.MethodGet, fmt.Sprintf("/api/users/%d/videos", u.ID), nil, 0)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestListVideos_Pagination(t *testing.T) {
	r, s := newTestServer(t)
	u, _ := s.CreateUser("u", "password123")
	for i := 0; i < 5; i++ {
		s.CreateVideo(u.ID, fmt.Sprintf("v%d", i), "/x.mp4", "")
	}
	w1 := do(t, r, http.MethodGet, "/api/videos?limit=3", nil, 0)
	m1 := parseBody(t, w1)
	cursor := int64(m1["data"].(map[string]interface{})["next_cursor"].(float64))
	items1 := m1["data"].(map[string]interface{})["items"].([]interface{})
	if len(items1) != 3 {
		t.Fatalf("page1: want 3, got %d", len(items1))
	}
	w2 := do(t, r, http.MethodGet, fmt.Sprintf("/api/videos?limit=3&max_id=%d", cursor), nil, 0)
	m2 := parseBody(t, w2)
	items2 := m2["data"].(map[string]interface{})["items"].([]interface{})
	if len(items2) != 2 {
		t.Fatalf("page2: want 2, got %d", len(items2))
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
