package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

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

func TestMiddleware_Allows(t *testing.T) {
	l := New(100, 100)
	handler := l.Middleware(KeyFromIP)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestMiddleware_Blocks(t *testing.T) {
	l := New(0, 0) // 限速为 0
	handler := l.Middleware(KeyFromIP)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d", w.Code)
	}
}

func TestMiddleware_EmptyKey(t *testing.T) {
	l := New(0, 0)
	called := false
	handler := l.Middleware(func(r *http.Request) string { return "" })(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called || w.Code != http.StatusOK {
		t.Fatal("empty key should bypass rate limit")
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

func TestKeyFromUserID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-User-Id", "42")
	key := KeyFromUserID(req)
	if key != "uid:42" {
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
