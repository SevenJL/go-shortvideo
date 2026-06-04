package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"shortvideo/internal/auth"
	"shortvideo/internal/store"
)

// Handler 持有处理请求所需的依赖。
type Handler struct {
	store     *store.Store
	uploadDir string
	jwtSecret string
}

func NewHandler(s *store.Store, uploadDir, jwtSecret string) *Handler {
	return &Handler{store: s, uploadDir: uploadDir, jwtSecret: jwtSecret}
}

// currentUserID 从请求 context 中解析"当前操作用户"(由 auth.Middleware 注入)。
// 同时兼容旧的 X-User-Id 头(用于未启用中间件的路由,如 GetVideo)。
func currentUserID(r *http.Request) (int64, bool) {
	// 优先从 auth 中间件注入的 context 取值
	if uid, ok := auth.UserIDFromContext(r.Context()); ok {
		return uid, true
	}
	// Fallback: 直接读 X-User-Id 头(用于不需要强鉴权的只读接口)
	raw := r.Header.Get("X-User-Id")
	if raw == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// pathID 从路径参数 {name} 解析 int64(依赖 Go 1.22+ 的 r.PathValue)。
func pathID(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(r.PathValue(name), 10, 64)
}

// queryInt 解析查询参数,失败或缺省时返回 def。
func queryInt(r *http.Request, name string, def int64) int64 {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return def
	}
	return v
}

// decodeJSON 解析请求体 JSON。
func decodeJSON(r *http.Request, dst interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(dst)
}

// storeErrStatus 把存储层错误映射为 HTTP 状态码。
func storeErrStatus(err error) int {
	switch err {
	case store.ErrNotFound:
		return http.StatusNotFound
	case store.ErrInvalid:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// requireUser 是需要登录态的处理器的统一前置:从 context 获取鉴权后的 userID,失败则返回 401。
func requireUser(w http.ResponseWriter, r *http.Request) (int64, bool) {
	uid, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "缺少认证信息: 请提供 Authorization: Bearer <token> 或 X-User-Id 头")
		return 0, false
	}
	return uid, true
}

// withLogging 简单访问日志中间件。
func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		log.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, sw.status, time.Since(start))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
