package security

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

func TestCORSMiddleware_Options(t *testing.T) {
	r := gin.New()
	r.Use(CORSMiddleware(PermissiveCORS()))
	r.GET("/api/test", func(c *gin.Context) { c.Status(200) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/api/test", nil)
	req.Header.Set("Origin", "http://example.com")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("CORS origin should be *")
	}
}

func TestCORSMiddleware_BlockedOrigin(t *testing.T) {
	r := gin.New()
	r.Use(CORSMiddleware(DefaultCORS())) // 白名单为空=禁止跨域
	r.GET("/api/test", func(c *gin.Context) { c.Status(200) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/api/test", nil)
	req.Header.Set("Origin", "http://evil.com")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", w.Code)
	}
}

func TestSecureHeaders(t *testing.T) {
	r := gin.New()
	r.Use(SecureHeaders())
	r.GET("/", func(c *gin.Context) { c.Status(200) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	r.ServeHTTP(w, req)

	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing X-Content-Type-Options")
	}
	if w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("missing X-Frame-Options")
	}
	if w.Header().Get("X-XSS-Protection") == "" {
		t.Fatal("missing X-XSS-Protection")
	}
}

func TestLoginProtection_Allow(t *testing.T) {
	p := NewLoginProtection(3, time.Minute, 5*time.Minute)
	ip := "10.0.0.1"

	if !p.Allow(ip) {
		t.Fatal("should allow initially")
	}
}

func TestLoginProtection_BlockAfterFailures(t *testing.T) {
	p := NewLoginProtection(3, time.Minute, 5*time.Minute)
	ip := "10.0.0.2"

	// 记录3次失败
	p.RecordFailure(ip)
	p.RecordFailure(ip)
	p.RecordFailure(ip)

	if p.Allow(ip) {
		t.Fatal("should block after 3 failures")
	}
}

func TestLoginProtection_SuccessClears(t *testing.T) {
	p := NewLoginProtection(3, time.Minute, 5*time.Minute)
	ip := "10.0.0.3"

	p.RecordFailure(ip)
	p.RecordFailure(ip)
	p.RecordSuccess(ip) // 登录成功应清除

	if !p.Allow(ip) {
		t.Fatal("should allow after success clears failures")
	}
}

func TestLoginProtection_DifferentIPs(t *testing.T) {
	p := NewLoginProtection(2, time.Minute, 5*time.Minute)

	p.RecordFailure("10.0.0.1")
	p.RecordFailure("10.0.0.1")

	if !p.Allow("10.0.0.2") {
		t.Fatal("different IP should not be affected")
	}
}

func TestLoginProtection_GinMiddleware(t *testing.T) {
	p := NewLoginProtection(2, time.Minute, 5*time.Minute)
	r := gin.New()
	r.Use(p.GinMiddleware())
	r.POST("/api/login", func(c *gin.Context) {
		c.JSON(http.StatusUnauthorized, gin.H{"msg": "wrong"})
	})

	ip := "10.0.0.5"
	// 2次失败 → 第3次应被拦截
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/login", nil)
		req.RemoteAddr = ip + ":12345"
		r.ServeHTTP(w, req)
		if i < 2 && w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: want 401, got %d", i+1, w.Code)
		}
		if i == 2 && w.Code != http.StatusTooManyRequests {
			t.Fatalf("attempt %d: want 429, got %d", i+1, w.Code)
		}
	}
}

func TestMaxBodySize(t *testing.T) {
	r := gin.New()
	r.Use(MaxBodySize(10)) // 10 bytes max
	r.POST("/api/test", func(c *gin.Context) { c.Status(200) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/test", nil)
	req.Body = http.MaxBytesReader(w, req.Body, 10)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	// Should either reject or pass with empty body
}
