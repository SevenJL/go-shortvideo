// Package metrics 提供轻量级 Prometheus 兼容的指标采集与 /metrics 端点。
// 无外部依赖，直接输出 Prometheus text format。
package metrics

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// ---- 指标类型 ----

// Counter 是单调递增的计数器。
type Counter struct {
	val  atomic.Int64
	name string
	help string
}

func NewCounter(name, help string) *Counter {
	c := &Counter{name: name, help: help}
	register(c)
	return c
}

func (c *Counter) Inc()         { c.val.Add(1) }
func (c *Counter) Add(n int64)  { c.val.Add(n) }
func (c *Counter) Value() int64 { return c.val.Load() }

// CounterVec 是带标签维度的计数器。
type CounterVec struct {
	mu     sync.Mutex
	counts map[string]*Counter
	name   string
	help   string
}

func NewCounterVec(name, help string, labels []string) *CounterVec {
	cv := &CounterVec{name: name, help: help, counts: make(map[string]*Counter)}
	register(cv)
	return cv
}

func (cv *CounterVec) WithLabelValues(vals ...string) *Counter {
	key := joinLabels(vals)
	cv.mu.Lock()
	defer cv.mu.Unlock()
	if c, ok := cv.counts[key]; ok {
		return c
	}
	c := &Counter{}
	cv.counts[key] = c
	return c
}

func (cv *CounterVec) snapshot() map[string]int64 {
	cv.mu.Lock()
	defer cv.mu.Unlock()
	m := make(map[string]int64, len(cv.counts))
	for k, c := range cv.counts {
		m[k] = c.Value()
	}
	return m
}

// Histogram 是延迟分布直方图，记录 count/sum 和分桶统计。
type Histogram struct {
	count   atomic.Int64
	sumUs   atomic.Int64 // 微秒（避免 float atomic）
	buckets []int64      // 桶边界（微秒）
	bucketVals []atomic.Int64 // 每个桶的计数
	name    string
	help    string
}

// 默认延迟桶（微秒）: 1ms, 5ms, 10ms, 50ms, 100ms, 500ms, 1s, 5s
var defaultBuckets = []int64{1000, 5000, 10000, 50000, 100000, 500000, 1000000, 5000000}

func NewHistogram(name, help string) *Histogram {
	return NewHistogramWithBuckets(name, help, defaultBuckets)
}

func NewHistogramWithBuckets(name, help string, bucketsUs []int64) *Histogram {
	h := &Histogram{
		name:       name,
		help:       help,
		buckets:    bucketsUs,
		bucketVals: make([]atomic.Int64, len(bucketsUs)),
	}
	register(h)
	return h
}

// Observe 记录一次观测（seconds），递增对应分桶。
func (h *Histogram) Observe(seconds float64) {
	us := int64(seconds * 1_000_000)
	h.count.Add(1)
	h.sumUs.Add(us)
	for i, bound := range h.buckets {
		if us <= bound {
			h.bucketVals[i].Add(1)
		}
	}
}

// Value 返回 count 和 sum（微秒）。
func (h *Histogram) Value() (count, sumUs int64) {
	return h.count.Load(), h.sumUs.Load()
}

// Buckets 返回桶边界和各桶累积值。
func (h *Histogram) BucketSnapshot() (bounds []int64, counts []int64) {
	bounds = make([]int64, len(h.buckets))
	counts = make([]int64, len(h.buckets))
	for i := range h.buckets {
		bounds[i] = h.buckets[i]
		counts[i] = h.bucketVals[i].Load()
	}
	return
}

// ---- 全局注册 ----

type metric interface {
	writeTo([]byte) []byte
}

var (
	mu       sync.Mutex
	registry []metric
)

func register(m metric) {
	mu.Lock()
	defer mu.Unlock()
	registry = append(registry, m)
}

func (c *Counter) writeTo(buf []byte) []byte {
	buf = append(buf, "# HELP "...)
	buf = append(buf, c.name...)
	buf = append(buf, ' ')
	buf = append(buf, c.help...)
	buf = append(buf, '\n')
	buf = append(buf, "# TYPE "...)
	buf = append(buf, c.name...)
	buf = append(buf, " counter\n"...)
	buf = append(buf, c.name...)
	buf = append(buf, ' ')
	buf = strconv.AppendInt(buf, c.Value(), 10)
	buf = append(buf, '\n', '\n')
	return buf
}

func (cv *CounterVec) writeTo(buf []byte) []byte {
	buf = append(buf, "# HELP "...)
	buf = append(buf, cv.name...)
	buf = append(buf, ' ')
	buf = append(buf, cv.help...)
	buf = append(buf, '\n')
	buf = append(buf, "# TYPE "...)
	buf = append(buf, cv.name...)
	buf = append(buf, " counter\n"...)
	for labels, count := range cv.snapshot() {
		buf = append(buf, cv.name...)
		if labels != "" {
			buf = append(buf, '{')
			buf = append(buf, labels...)
			buf = append(buf, '}')
		}
		buf = append(buf, ' ')
		buf = strconv.AppendInt(buf, count, 10)
		buf = append(buf, '\n')
	}
	buf = append(buf, '\n')
	return buf
}

func (h *Histogram) writeTo(buf []byte) []byte {
	count, sumUs := h.Value()
	bounds, buckCounts := h.BucketSnapshot()

	buf = append(buf, "# HELP "...)
	buf = append(buf, h.name...)
	buf = append(buf, ' ')
	buf = append(buf, h.help...)
	buf = append(buf, '\n')
	buf = append(buf, "# TYPE "...)
	buf = append(buf, h.name...)
	buf = append(buf, " histogram\n"...)

	// 各分桶 (le="bound")
	var cumulative int64
	for i, bound := range bounds {
		cumulative += buckCounts[i]
		buf = append(buf, h.name...)
		buf = append(buf, "_bucket{le=\""...)
		buf = strconv.AppendFloat(buf, float64(bound)/1_000_000, 'f', 3, 64)
		buf = append(buf, "\"} "...)
		buf = strconv.AppendInt(buf, cumulative, 10)
		buf = append(buf, '\n')
	}
	// +Inf bucket
	buf = append(buf, h.name...)
	buf = append(buf, "_bucket{le=\"+Inf\"} "...)
	buf = strconv.AppendInt(buf, count, 10)
	buf = append(buf, '\n')

	// count + sum
	buf = append(buf, h.name...)
	buf = append(buf, "_count "...)
	buf = strconv.AppendInt(buf, count, 10)
	buf = append(buf, '\n')
	buf = append(buf, h.name...)
	buf = append(buf, "_sum "...)
	buf = strconv.AppendFloat(buf, float64(sumUs)/1_000_000, 'f', 6, 64)
	buf = append(buf, '\n', '\n')
	return buf
}

// ---- 预定义指标 ----

var (
	HTTPRequests = NewCounterVec(
		"http_requests_total",
		"Total number of HTTP requests.",
		[]string{"method", "path", "status"},
	)
	HTTPDuration = NewCounterVec(
		"http_request_duration_microseconds",
		"HTTP request latency in microseconds (count only).",
		[]string{"method", "path"},
	)
	LikeOps = NewCounterVec(
		"like_operations_total",
		"Total number of like/unlike operations.",
		[]string{"action", "source"},
	)
	FanoutOps = NewCounterVec(
		"fanout_operations_total",
		"Fanout write diffusion operations.",
		[]string{"status"},
	)
	FeedMergeOps = NewCounterVec(
		"feed_merge_operations_total",
		"Following feed merge operations.",
		[]string{},
	)
	FanoutHistogram    *Histogram
	FeedMergeHistogram *Histogram
	DBErrors = NewCounterVec(
		"db_errors_total",
		"Total number of database errors.",
		[]string{"operation"},
	)
)

func init() {
	FanoutHistogram = NewHistogramWithBuckets(
		"fanout_duration_seconds",
		"Fanout write diffusion latency in seconds.",
		[]int64{10_000, 50_000, 100_000, 500_000, 1_000_000, 5_000_000},
	)
	FeedMergeHistogram = NewHistogramWithBuckets(
		"feed_merge_duration_seconds",
		"Following feed merge (push+pull) latency in seconds.",
		[]int64{1000, 5000, 10_000, 50_000, 100_000, 500_000},
	)
}

// RecordLatency 记录延迟，同时递增计数器（作为简化指标）。
func RecordLatency(cv *CounterVec, method, path string, dur time.Duration) {
	cv.WithLabelValues(method, path).Add(dur.Microseconds())
}

// ---- HTTP 处理器 ----

// Handler 返回 /metrics 端点，输出 Prometheus text format。
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		mu.Lock()
		defer mu.Unlock()
		var buf []byte
		for _, m := range registry {
			buf = m.writeTo(buf)
		}
		w.Write(buf)
	})
}

// Middleware 记录每个 HTTP 请求的计数和延迟（标准库）。
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		dur := time.Since(start)
		status := strconv.Itoa(sw.status)
		HTTPRequests.WithLabelValues(r.Method, r.URL.Path, status).Inc()
		RecordLatency(HTTPDuration, r.Method, r.URL.Path, dur)
	})
}

// GinMiddleware 记录每个 Gin 请求的计数和延迟。
func GinMiddleware() func(c *gin.Context) {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		dur := time.Since(start)
		status := strconv.Itoa(c.Writer.Status())
		HTTPRequests.WithLabelValues(c.Request.Method, c.Request.URL.Path, status).Inc()
		RecordLatency(HTTPDuration, c.Request.Method, c.Request.URL.Path, dur)
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func init() {
	// 注册 gin 所需的类型断言
	var _ = gin.DefaultWriter
}

// ---- 工具 ----

func joinLabels(vals []string) string {
	if len(vals) == 0 {
		return ""
	}
	s := vals[0]
	for _, v := range vals[1:] {
		s += "," + v
	}
	return s
}

// 确保 fmt 被使用（生成的 writeTo 方法中用到）
var _ = fmt.Sprintf

