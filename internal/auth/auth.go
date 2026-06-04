// Package auth 提供 JWT 令牌生成/验证与 Gin 鉴权中间件。
package auth

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const UserIDKey contextKey = "user_id"

var (
	ErrNoToken      = errors.New("缺少认证信息")
	ErrInvalidToken = errors.New("无效或过期的令牌")
)

type JWT struct {
	secret []byte
}

func NewJWT(secret string) *JWT {
	return &JWT{secret: []byte(secret)}
}

type claims struct {
	jwt.RegisteredClaims
	UserID int64 `json:"uid"`
}

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

func (j *JWT) ValidateToken(tokenStr string) (int64, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &claims{},
		func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
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

// GinMiddleware 返回 Gin 鉴权中间件。
// 优先 Authorization: Bearer <token>，fallback X-User-Id 头。
func (j *JWT) GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. JWT Bearer
		authHeader := c.GetHeader("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			uid, err := j.ValidateToken(tokenStr)
			if err == nil {
				c.Set("user_id", uid)
				c.Next()
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": 401, "msg": ErrInvalidToken.Error(),
			})
			return
		}

		// 2. X-User-Id fallback
		if raw := c.GetHeader("X-User-Id"); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err == nil && id > 0 {
				c.Set("user_id", id)
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"code": 401, "msg": "缺少认证信息",
		})
	}
}

// UserIDFromContext 从 Gin context 中提取 userID。
func UserIDFromContext(c *gin.Context) (int64, bool) {
	v, exists := c.Get("user_id")
	if !exists {
		return 0, false
	}
	id, ok := v.(int64)
	return id, ok && id > 0
}

// UserIDFromStdContext 从标准 context.Context 提取（供非 Gin 包使用）。
func UserIDFromStdContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(UserIDKey).(int64)
	return id, ok && id > 0
}
