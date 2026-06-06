package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/time/rate"
)

func init() { gin.SetMode(gin.TestMode) }

func TestLimiter_Allow(t *testing.T) {
	l := New(10, 20)

	// 前 20 个应通过（burst 容量）
	for i := 0; i < 20; i++ {
		if !l.Allow("user1") {
			t.Fatalf("request %d should be allowed (within burst)", i)
		}
	}
	// 第 21 个应被限
	if l.Allow("user1") {
		t.Fatal("request 21 should be rate limited")
	}
}

func TestLimiter_DifferentKeys(t *testing.T) {
	l := New(1, 1)

	// user1 用完额度
	l.Allow("user1")

	// user2 应不受影响
	if !l.Allow("user2") {
		t.Fatal("user2 should have their own bucket")
	}
	// user1 应被限
	if l.Allow("user1") {
		t.Fatal("user1 should be limited")
	}
}

func TestLimiter_Cleanup(t *testing.T) {
	l := New(100, 100)
	l.ttl = 1 * time.Millisecond // 快速过期

	l.Allow("user1")
	time.Sleep(5 * time.Millisecond)
	l.Cleanup()

	l.mu.Lock()
	_, exists := l.visitors["user1"]
	l.mu.Unlock()
	if exists {
		t.Fatal("user1 should have been cleaned up")
	}
}

func TestPathLimiter_Middleware(t *testing.T) {
	pl := NewPathLimiter(map[string][2]int{"/api/videos": {100, 200}}, New(500, 1000))
	handler := pl.Middleware(KeyFromIP)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/videos/1/like", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestPathLimiter_Blocks(t *testing.T) {
	pl := NewPathLimiter(map[string][2]int{"/test": {0, 1}}, nil)
	handler := pl.Middleware(func(r *http.Request) string { return "test-key" })(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "1.1.1.1:1234"
	// First request passes (burst=1)
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req)
	// Second request blocked (rate=0)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d", w2.Code)
	}
}

func TestPathLimiter_EmptyKey(t *testing.T) {
	pl := NewPathLimiter(map[string][2]int{"/test": {0, 1}}, nil)
	called := false
	handler := pl.Middleware(func(r *http.Request) string { return "" })(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}),
	)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if !called || w.Code != http.StatusOK {
		t.Fatal("empty key should bypass")
	}
}

func TestKeyFromIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	key := KeyFromIP(req)
	if key != "ip:192.168.1.1:12345" {
		t.Fatalf("unexpected key: %s", key)
	}
}

func TestKeyFromIP_XForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "10.0.0.1")
	key := KeyFromIP(req)
	if key != "ip:10.0.0.1" {
		t.Fatalf("unexpected key: %s", key)
	}
}

func TestKeyFromUserIDGin(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("X-User-Id", "42")
	key := KeyFromUserID(c)
	if key != "uid:42" {
		t.Fatalf("unexpected key: %s", key)
	}
}

func TestKeyFromJWTOrUserID(t *testing.T) {
	secret := "test-secret"
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"uid": float64(99),
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenStr, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Authorization", "Bearer "+tokenStr)
	key := KeyFromJWTOrUserID(secret)(c)
	if key != "uid:99" {
		t.Fatalf("unexpected key: %s", key)
	}
}

func TestKeyFromJWTOrUserID_DisablesXUserIDFallback(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.RemoteAddr = "10.0.0.1:1234"
	c.Request.Header.Set("X-User-Id", "42")
	c.Set("allow_x_user_id", false)

	key := KeyFromJWTOrUserID("secret")(c)
	if key != "ip:10.0.0.1" {
		t.Fatalf("unexpected key: %s", key)
	}
}

func TestKeyFromIPGin(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.RemoteAddr = "10.0.0.1:1234"
	key := KeyFromIPGin(c)
	if key != "ip:10.0.0.1" {
		t.Fatalf("unexpected key: %s", key)
	}
}

func TestFormatInt(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{-1, "-1"},
		{1234567890, "1234567890"},
	}
	for _, c := range cases {
		got := formatInt(c.n)
		if got != c.want {
			t.Fatalf("formatInt(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestNew_DefaultRate(t *testing.T) {
	l := New(50, 100)
	if l.rate != 50 {
		t.Fatalf("want rate=50, got %v", l.rate)
	}
	if l.burst != 100 {
		t.Fatalf("want burst=100, got %d", l.burst)
	}
	if l.rate != rate.Limit(50) {
		t.Fatal("rate type mismatch")
	}
}
