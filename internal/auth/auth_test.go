package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func init() { gin.SetMode(gin.TestMode) }

const testSecret = "test-secret"

func TestGenerateAndValidate(t *testing.T) {
	j := NewJWT(testSecret)
	tok, err := j.GenerateToken(42)
	if err != nil || tok == "" {
		t.Fatalf("generate: %v", err)
	}
	uid, err := j.ValidateToken(tok)
	if err != nil || uid != 42 {
		t.Fatalf("validate: uid=%d err=%v", uid, err)
	}
}

func TestValidateExpiredToken(t *testing.T) {
	j := NewJWT(testSecret)
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
		t.Fatalf("expected ErrInvalidToken, got %v", err)
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
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestValidateToken_WrongSecret(t *testing.T) {
	j1 := NewJWT("a")
	j2 := NewJWT("b")
	tok, _ := j1.GenerateToken(1)
	_, err := j2.ValidateToken(tok)
	if err != ErrInvalidToken {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func ginTestCtx(t *testing.T, bearerToken, xUserID string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if bearerToken != "" {
		c.Request.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	if xUserID != "" {
		c.Request.Header.Set("X-User-Id", xUserID)
	}
	return c, w
}

func TestGinMiddleware_BearerToken(t *testing.T) {
	j := NewJWT(testSecret)
	tok, _ := j.GenerateToken(7)
	c, w := ginTestCtx(t, tok, "")

	called := false
	j.GinMiddleware()(c)
	if !c.IsAborted() {
		called = true
		c.Status(http.StatusOK)
	}
	if !called {
		t.Fatal("handler should have been called")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	uid, _ := c.Get("user_id")
	if uid.(int64) != 7 {
		t.Fatalf("want uid 7, got %v", uid)
	}
}

func TestGinMiddleware_FallbackXUserId(t *testing.T) {
	j := NewJWT(testSecret)
	c, _ := ginTestCtx(t, "", "99")

	called := false
	j.GinMiddleware()(c)
	if !c.IsAborted() {
		called = true
		c.Status(http.StatusOK)
	}
	if !called {
		t.Fatal("handler should have been called")
	}
	uid, _ := c.Get("user_id")
	if uid.(int64) != 99 {
		t.Fatalf("want 99, got %v", uid)
	}
}

func TestGinMiddleware_NoAuth(t *testing.T) {
	j := NewJWT(testSecret)
	c, w := ginTestCtx(t, "", "")

	j.GinMiddleware()(c)
	if !c.IsAborted() {
		t.Fatal("should abort without auth")
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestGinMiddleware_BearerPriority(t *testing.T) {
	j := NewJWT(testSecret)
	tok, _ := j.GenerateToken(42)
	c, _ := ginTestCtx(t, tok, "99")

	called := false
	j.GinMiddleware()(c)
	if !c.IsAborted() {
		called = true
		c.Status(http.StatusOK)
	}
	if !called {
		t.Fatal("handler should have been called")
	}
	uid, _ := c.Get("user_id")
	if uid.(int64) != 42 {
		t.Fatalf("JWT should take priority: want 42, got %v", uid)
	}
}

func TestGinMiddleware_InvalidJWT_NoFallback(t *testing.T) {
	j := NewJWT(testSecret)
	c, w := ginTestCtx(t, "invalid.token", "99")

	j.GinMiddleware()(c)
	if !c.IsAborted() {
		t.Fatal("should abort with invalid JWT")
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}
