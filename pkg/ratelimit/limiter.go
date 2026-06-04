// Package ratelimit 提供基于令牌桶的 HTTP 限流中间件（支持标准库和 Gin）。
package ratelimit

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// Limiter 按 key 进行令牌桶限流。
type Limiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     rate.Limit
	burst    int
	ttl      time.Duration
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func New(r rate.Limit, burst int) *Limiter {
	return &Limiter{
		visitors: make(map[string]*visitor),
		rate:     r,
		burst:    burst,
		ttl:      5 * time.Minute,
	}
}

func (l *Limiter) Allow(key string) bool {
	return l.getLimiter(key).Allow()
}

func (l *Limiter) getLimiter(key string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.visitors[key]
	if !ok {
		lim := rate.NewLimiter(l.rate, l.burst)
		l.visitors[key] = &visitor{limiter: lim, lastSeen: time.Now()}
		return lim
	}
	v.lastSeen = time.Now()
	return v.limiter
}

func (l *Limiter) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, v := range l.visitors {
		if time.Since(v.lastSeen) > l.ttl {
			delete(l.visitors, key)
		}
	}
}

// PathLimiter 按 URL 路径前缀分配不同的限流器。
type PathLimiter struct {
	limiters map[string]*Limiter
	fallback *Limiter
}

func NewPathLimiter(rules map[string][2]int, fallback *Limiter) *PathLimiter {
	pl := &PathLimiter{
		limiters: make(map[string]*Limiter),
		fallback: fallback,
	}
	for prefix, cfg := range rules {
		pl.limiters[prefix] = New(rate.Limit(cfg[0]), cfg[1])
	}
	return pl
}

func (pl *PathLimiter) Allow(path, key string) bool {
	for prefix, lim := range pl.limiters {
		if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
			return lim.Allow(key)
		}
	}
	if pl.fallback != nil {
		return pl.fallback.Allow(key)
	}
	return true
}

func (pl *PathLimiter) Cleanup() {
	for _, lim := range pl.limiters {
		lim.Cleanup()
	}
	if pl.fallback != nil {
		pl.fallback.Cleanup()
	}
}

// Middleware 返回标准库 HTTP 限流中间件。
func (pl *PathLimiter) Middleware(keyFn func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFn(r)
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}
			if !pl.Allow(r.URL.Path, key) {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"code":429,"msg":"请求过于频繁，请稍后再试"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// GinMiddleware 返回 Gin 框架的限流中间件。
func (pl *PathLimiter) GinMiddleware(keyFn func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := keyFn(c)
		if key == "" {
			c.Next()
			return
		}
		if !pl.Allow(c.Request.URL.Path, key) {
			c.Header("Retry-After", "1")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"code": 429, "msg": "请求过于频繁，请稍后再试",
			})
			return
		}
		c.Next()
	}
}

func DefaultPathRules() map[string][2]int {
	return map[string][2]int{
		"/api/login":  {5, 10},
		"/api/upload": {10, 15},
		"/api/videos": {100, 200},
		"/api/feed":   {50, 100},
		"/api/users":  {20, 50},
	}
}

func KeyFromUserID(c *gin.Context) string {
	if uid, ok := c.Get("user_id"); ok {
		if id, ok := uid.(int64); ok && id > 0 {
			return "uid:" + formatInt(id)
		}
	}
	if raw := c.GetHeader("X-User-Id"); raw != "" {
		return "uid:" + raw
	}
	return KeyFromIPGin(c)
}

func KeyFromIPGin(c *gin.Context) string {
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		return "ip:" + xff
	}
	return "ip:" + c.ClientIP()
}

func KeyFromIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return "ip:" + xff
	}
	return "ip:" + r.RemoteAddr
}

func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
