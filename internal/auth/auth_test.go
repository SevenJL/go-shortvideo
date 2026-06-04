package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-secret"

func TestGenerateAndValidate(t *testing.T) {
	j := NewJWT(testSecret)
	tok, err := j.GenerateToken(42)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if tok == "" {
		t.Fatal("token should not be empty")
	}

	uid, err := j.ValidateToken(tok)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if uid != 42 {
		t.Fatalf("want uid 42, got %d", uid)
	}
}

func TestValidateExpiredToken(t *testing.T) {
	j := NewJWT(testSecret)

	// 手动构造一个已过期的 token
	now := time.Now()
	c := claims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(now.Add(-1 * time.Hour)),
		},
		UserID: 1,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	tok, _ := token.SignedString(j.secret)

	_, err := j.ValidateToken(tok)
	if err != ErrInvalidToken {
		t.Fatalf("expected ErrInvalidToken for expired token, got %v", err)
	}
}

func TestValidateMalformedToken(t *testing.T) {
	j := NewJWT(testSecret)

	_, err := j.ValidateToken("not.a.token")
	if err != ErrInvalidToken {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}

	_, err = j.ValidateToken("")
	if err != ErrInvalidToken {
		t.Fatalf("expected ErrInvalidToken for empty token, got %v", err)
	}
}

func TestValidateToken_WrongSecret(t *testing.T) {
	j1 := NewJWT("secret-a")
	j2 := NewJWT("secret-b")

	tok, _ := j1.GenerateToken(1)
	_, err := j2.ValidateToken(tok)
	if err != ErrInvalidToken {
		t.Fatalf("expected ErrInvalidToken for wrong secret, got %v", err)
	}
}

func TestAuthMiddleware_BearerToken(t *testing.T) {
	j := NewJWT(testSecret)
	tok, _ := j.GenerateToken(7)

	var gotUID int64
	handler := j.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, _ := UserIDFromContext(r.Context())
		gotUID = uid
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if gotUID != 7 {
		t.Fatalf("want uid 7, got %d", gotUID)
	}
}

func TestAuthMiddleware_FallbackXUserId(t *testing.T) {
	j := NewJWT(testSecret)

	var gotUID int64
	handler := j.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, _ := UserIDFromContext(r.Context())
		gotUID = uid
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-User-Id", "99")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if gotUID != 99 {
		t.Fatalf("want uid 99, got %d", gotUID)
	}
}

func TestAuthMiddleware_NoAuth(t *testing.T) {
	j := NewJWT(testSecret)

	handler := j.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_BearerPriority(t *testing.T) {
	// JWT 优先于 X-User-Id
	j := NewJWT(testSecret)
	tok, _ := j.GenerateToken(42)

	var gotUID int64
	handler := j.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, _ := UserIDFromContext(r.Context())
		gotUID = uid
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-User-Id", "99") // 应被忽略
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if gotUID != 42 {
		t.Fatalf("JWT should take priority: want 42, got %d", gotUID)
	}
}

func TestAuthMiddleware_InvalidJWT_NoFallback(t *testing.T) {
	// 无效 JWT 不应 fallback 到 X-User-Id
	j := NewJWT(testSecret)

	handler := j.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called with invalid JWT")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.here")
	req.Header.Set("X-User-Id", "99") // 不应被使用
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for invalid JWT, got %d", w.Code)
	}
}

func TestUserIDFromContext_NoValue(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, ok := UserIDFromContext(req.Context())
	if ok {
		t.Fatal("expected false when no userID in context")
	}
}
