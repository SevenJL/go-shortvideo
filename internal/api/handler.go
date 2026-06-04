package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"shortvideo/internal/store"
)

// Handler 持有处理请求所需的依赖。
type Handler struct {
	store     *store.Store
	uploadDir string
}

func NewHandler(s *store.Store, uploadDir string) *Handler {
	return &Handler{store: s, uploadDir: uploadDir}
}

// currentUserID 从 X-User-Id 请求头解析"当前操作用户"。
// 这是为演示而做的极简鉴权,真实项目应替换为 JWT / Session。
func currentUserID(r *http.Request) (int64, bool) {
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

// requireUser 是需要登录态的处理器的统一前置:解析 X-User-Id,失败则返回 401。
func requireUser(w http.ResponseWriter, r *http.Request) (int64, bool) {
	uid, ok := currentUserID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "缺少或非法的 X-User-Id 请求头")
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
