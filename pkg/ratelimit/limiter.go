// Package ratelimit 提供基于令牌桶的 HTTP 限流中间件。
package ratelimit

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limiter 按 key（用户ID 或 IP）进行令牌桶限流。
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

// New 创建限流器。r 为每秒允许的请求数，burst 为突发容量。
func New(r rate.Limit, burst int) *Limiter {
	return &Limiter{
		visitors: make(map[string]*visitor),
		rate:     r,
		burst:    burst,
		ttl:      5 * time.Minute,
	}
}

// Allow 判断 key 是否被允许通过。
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

// Cleanup 清理过期的限流器记录，应定期调用（如每分钟）。
func (l *Limiter) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, v := range l.visitors {
		if time.Since(v.lastSeen) > l.ttl {
			delete(l.visitors, key)
		}
	}
}

// Middleware 返回 HTTP 限流中间件。
// keyFn 从请求中提取限流 key（如用户ID 或 IP）。
func (l *Limiter) Middleware(keyFn func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFn(r)
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}
			if !l.Allow(key) {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"code": 429,
					"msg":  "请求过于频繁，请稍后再试",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// PathLimiter 按 URL 路径前缀分配不同的限流器。
// 适用于: 登录接口限 10/s，点赞接口限 100/s，上传接口限 5/s。
type PathLimiter struct {
	limiters map[string]*Limiter
	fallback *Limiter
}

type pathRule struct {
	prefix  string
	limiter *Limiter
}

// NewPathLimiter 创建按路径限流器。
// rules: 路径前缀 → 限流参数。fallback 用于不匹配任何路径的请求。
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

// Allow 判断路径是否允许通过。
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

// Cleanup 清理所有子限流器的过期记录。
func (pl *PathLimiter) Cleanup() {
	for _, lim := range pl.limiters {
		lim.Cleanup()
	}
	if pl.fallback != nil {
		pl.fallback.Cleanup()
	}
}

// Middleware 返回按路径区分的 HTTP 限流中间件。
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

// DefaultPathRules 返回推荐的各接口限流配置。
// 格式: map[路径前缀][2]int{QPS, Burst}
func DefaultPathRules() map[string][2]int {
	return map[string][2]int{
		"/api/login":   {5, 10},    // 登录: 5 QPS（防暴力破解）
		"/api/upload":  {10, 15},   // 上传: 10 QPS（大文件限速）
		"/api/videos":  {100, 200}, // 视频操作: 100 QPS
		"/api/feed":    {50, 100},  // 关注流: 50 QPS
		"/api/users":   {20, 50},   // 用户操作: 20 QPS
	}
}

// KeyFromUserID 从 context 或 X-User-Id 头提取用户 ID 作为限流 key。
func KeyFromUserID(r *http.Request) string {
	// 优先从 auth middleware 注入的 context
	if uid, ok := r.Context().Value("user_id").(int64); ok && uid > 0 {
		return "uid:" + formatInt(uid)
	}
	if raw := r.Header.Get("X-User-Id"); raw != "" {
		return "uid:" + raw
	}
	// fallback to IP
	return KeyFromIP(r)
}

// KeyFromIP 从请求中提取 IP 地址作为限流 key。
func KeyFromIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return "ip:" + xff
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return "ip:" + xri
	}
	return "ip:" + r.RemoteAddr
}

func formatInt(n int64) string {
	// 简单格式化，避免 import fmt 的大开销（限流热路径）
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
