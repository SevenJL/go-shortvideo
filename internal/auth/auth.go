// Package auth 提供 JWT 令牌生成/验证与 HTTP 鉴权中间件。
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Context key 类型，避免 key 冲突。
type contextKey string

const UserIDKey contextKey = "user_id"

var (
	ErrNoToken      = errors.New("缺少认证信息")
	ErrInvalidToken = errors.New("无效或过期的令牌")
)

// JWT 封装 JWT 的生成与验证。
type JWT struct {
	secret []byte
}

// NewJWT 创建 JWT 实例。
func NewJWT(secret string) *JWT {
	return &JWT{secret: []byte(secret)}
}

// claims 自定义 JWT 声明。
type claims struct {
	jwt.RegisteredClaims
	UserID int64 `json:"uid"`
}

// GenerateToken 为用户签发 JWT，默认 24 小时过期。
func (j *JWT) GenerateToken(userID int64) (string, error) {
	now := time.Now()
	c := claims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
		},
		UserID: userID,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return token.SignedString(j.secret)
}

// ValidateToken 验证并返回 token 中的 userID。
func (j *JWT) ValidateToken(tokenStr string) (int64, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &claims{},
		func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return j.secret, nil
		},
	)
	if err != nil {
		return 0, ErrInvalidToken
	}
	c, ok := token.Claims.(*claims)
	if !ok || !token.Valid {
		return 0, ErrInvalidToken
	}
	return c.UserID, nil
}

// Middleware 返回 HTTP 鉴权中间件。
//
// 鉴权策略（优先级从高到低）：
//  1. Authorization: Bearer <token> — JWT 验证
//  2. X-User-Id: <id> — 开发/测试降级通道（仅当无 JWT 时生效）
func (j *JWT) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. 优先解析 JWT
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			uid, err := j.ValidateToken(tokenStr)
			if err == nil {
				ctx := context.WithValue(r.Context(), UserIDKey, uid)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			// JWT 存在但无效 — 直接拒绝，不 fallback
			writeAuthErr(w, ErrInvalidToken.Error())
			return
		}

		// 2. Fallback: X-User-Id（仅开发/测试用）
		if raw := r.Header.Get("X-User-Id"); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err == nil && id > 0 {
				ctx := context.WithValue(r.Context(), UserIDKey, id)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		// 3. 无任何鉴权信息
		writeAuthErr(w, "缺少认证信息: 请提供 Authorization: Bearer <token> 或 X-User-Id 头")
	})
}

// UserIDFromContext 从 context 中提取经过鉴权的 userID。
func UserIDFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(UserIDKey).(int64)
	return id, ok && id > 0
}

// writeAuthErr 输出统一的鉴权错误 JSON。
func writeAuthErr(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	fmt.Fprintf(w, `{"code":401,"msg":"%s"}`+"\n", msg)
}
