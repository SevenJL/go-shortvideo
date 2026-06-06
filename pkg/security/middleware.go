// Package security 提供 CORS、安全响应头、防暴力破解等 HTTP 安全中间件。
package security

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ============================================
// CORS 中间件
// ============================================

// CORSConfig CORS 配置。
type CORSConfig struct {
	AllowOrigins     []string      // 允许的来源，空=允许所有
	AllowMethods     []string      // 允许的方法
	AllowHeaders     []string      // 允许的请求头
	ExposeHeaders    []string      // 暴露的响应头
	AllowCredentials bool          // 是否允许携带 Cookie
	MaxAge           time.Duration // 预检缓存时间
}

// DefaultCORS 生产环境默认 CORS 配置（仅允许白名单域名）。
func DefaultCORS() CORSConfig {
	return CORSConfig{
		AllowOrigins:     []string{}, // 空=生产环境应配置具体域名
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-User-Id", "X-Requested-With"},
		ExposeHeaders:    []string{"Content-Length", "X-Request-Id"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}
}

// PermissiveCORS 开发环境 CORS（允许所有来源，仅用于本地开发）。
func PermissiveCORS() CORSConfig {
	return CORSConfig{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowHeaders:     []string{"*"},
		AllowCredentials: false,
		MaxAge:           12 * time.Hour,
	}
}

// CORSMiddleware 返回 Gin CORS 中间件。
func CORSMiddleware(cfg CORSConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		// 检查来源是否允许
		// 空 AllowOrigins = 禁止所有跨域; ["*"] = 允许所有
		allowed := false
		for _, o := range cfg.AllowOrigins {
			if o == "*" || o == origin {
				allowed = true
				break
			}
		}
		// 有 Origin 头但不在白名单 → 拒绝预检
		if !allowed && origin != "" {
			if c.Request.Method == "OPTIONS" {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.Next()
			return
		}

		// 设置 CORS 头
		if len(cfg.AllowOrigins) == 1 && cfg.AllowOrigins[0] == "*" {
			c.Header("Access-Control-Allow-Origin", "*")
		} else if origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		}

		if cfg.AllowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}

		// 预检请求
		if c.Request.Method == "OPTIONS" {
			c.Header("Access-Control-Allow-Methods", join(cfg.AllowMethods))
			c.Header("Access-Control-Allow-Headers", join(cfg.AllowHeaders))
			c.Header("Access-Control-Expose-Headers", join(cfg.ExposeHeaders))
			c.Header("Access-Control-Max-Age", itoa(int(cfg.MaxAge.Seconds())))
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// ============================================
// 安全响应头中间件
// ============================================

// SecureHeaders 添加 OWASP 推荐的安全响应头。
func SecureHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 防止 MIME 类型嗅探
		c.Header("X-Content-Type-Options", "nosniff")
		// 防止点击劫持
		c.Header("X-Frame-Options", "DENY")
		// 启用浏览器 XSS 过滤器
		c.Header("X-XSS-Protection", "1; mode=block")
		// 引用策略（防止敏感信息泄露）
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		// 内容安全策略（CSP）
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; media-src 'self'")
		// 仅 HTTPS（生产环境应启用）
		// c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		// 权限策略
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

		c.Next()
	}
}

// ============================================
// 登录暴力破解防护
// ============================================

// LoginProtection 基于 IP 的登录失败计数器。
type LoginProtection struct {
	mu          sync.Mutex
	failures    map[string]*failRecord
	maxFailures int           // 最大失败次数
	window      time.Duration // 计数窗口
	blockTime   time.Duration // 封禁时间
}

type failRecord struct {
	count    int
	firstTry time.Time
	blockedUntil time.Time
}

// NewLoginProtection 创建登录保护器。
// maxFailures: 窗口内最大失败次数
// window: 失败计数窗口
// blockTime: 超过阈值后的封禁时间
func NewLoginProtection(maxFailures int, window, blockTime time.Duration) *LoginProtection {
	return &LoginProtection{
		failures:    make(map[string]*failRecord),
		maxFailures: maxFailures,
		window:      window,
		blockTime:   blockTime,
	}
}

// Allow 检查 IP 是否允许尝试登录。返回 true 表示允许。
func (p *LoginProtection) Allow(ip string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.failures[ip]
	if !ok {
		return true
	}
	// 窗口过期，重置
	if time.Since(r.firstTry) > p.window {
		delete(p.failures, ip)
		return true
	}
	// 封禁中
	if !r.blockedUntil.IsZero() && time.Now().Before(r.blockedUntil) {
		return false
	}
	// 封禁过期，重置
	if !r.blockedUntil.IsZero() && time.Now().After(r.blockedUntil) {
		delete(p.failures, ip)
		return true
	}
	return true
}

// RecordFailure 记录一次登录失败。
func (p *LoginProtection) RecordFailure(ip string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	r, ok := p.failures[ip]
	if !ok || time.Since(r.firstTry) > p.window {
		p.failures[ip] = &failRecord{count: 1, firstTry: now}
		return
	}
	r.count++
	if r.count >= p.maxFailures {
		r.blockedUntil = now.Add(p.blockTime)
	}
}

// RecordSuccess 登录成功，清除失败记录。
func (p *LoginProtection) RecordSuccess(ip string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.failures, ip)
}

// GinMiddleware 返回 Gin 中间件，仅对 /api/login 生效。
func (p *LoginProtection) GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path != "/api/login" || c.Request.Method != "POST" {
			c.Next()
			return
		}
		ip := c.ClientIP()
		if !p.Allow(ip) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"code": 429,
				"msg":  "登录尝试过于频繁，请稍后再试",
			})
			return
		}
		c.Next()
		// 登录失败时记录（Gin 的 c.Writer.Status() 在 handler 返回后立即可读）
		status := c.Writer.Status()
		if status == http.StatusUnauthorized {
			p.RecordFailure(ip)
		} else if status == http.StatusOK {
			p.RecordSuccess(ip)
		}
	}
}

// Cleanup 清理过期的失败记录，应定期调用。
func (p *LoginProtection) Cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for ip, r := range p.failures {
		if time.Since(r.firstTry) > p.window+p.blockTime {
			delete(p.failures, ip)
		}
	}
}

// ============================================
// 请求大小限制
// ============================================

// MaxBodySize 限制请求体大小（防 OOM 攻击）。
func MaxBodySize(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}

// ============================================
// 工具函数
// ============================================

func join(ss []string) string {
	if len(ss) == 0 { return "" }
	s := ss[0]
	for _, v := range ss[1:] { s += ", " + v }
	return s
}

func itoa(n int) string {
	if n == 0 { return "0" }
	var buf [12]byte
	i := len(buf)
	for n > 0 { i--; buf[i] = byte('0'+n%10); n /= 10 }
	return string(buf[i:])
}
