// Package metrics wraps the official Prometheus client used by the service.
package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Counter struct {
	val atomic.Int64
	pc  prometheus.Counter
}

func NewCounter(name, help string) *Counter {
	c := &Counter{pc: prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: help})}
	_ = prometheus.Register(c.pc)
	return c
}

func (c *Counter) Inc() { c.Add(1) }
func (c *Counter) Add(n int64) {
	c.val.Add(n)
	if c.pc != nil {
		c.pc.Add(float64(n))
	}
}
func (c *Counter) Value() int64 { return c.val.Load() }

type CounterVec struct {
	mu      sync.Mutex
	counts  map[string]*Counter
	labels  []string
	promVec *prometheus.CounterVec
}

func NewCounterVec(name, help string, labels []string) *CounterVec {
	cv := &CounterVec{
		counts:  make(map[string]*Counter),
		labels:  labels,
		promVec: prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels),
	}
	_ = prometheus.Register(cv.promVec)
	return cv
}

func (cv *CounterVec) WithLabelValues(vals ...string) *Counter {
	vals = normalizeLabels(vals, len(cv.labels))
	key := joinLabels(vals)
	cv.mu.Lock()
	defer cv.mu.Unlock()
	if c, ok := cv.counts[key]; ok {
		return c
	}
	c := &Counter{}
	if cv.promVec != nil {
		c.pc = cv.promVec.WithLabelValues(vals...)
	}
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

type Histogram struct {
	count      atomic.Int64
	sumUs      atomic.Int64
	buckets    []int64
	bucketVals []atomic.Int64
	ph         prometheus.Histogram
}

var defaultBuckets = []int64{1000, 5000, 10000, 50000, 100000, 500000, 1000000, 5000000}

func NewHistogram(name, help string) *Histogram {
	return NewHistogramWithBuckets(name, help, defaultBuckets)
}

func NewHistogramWithBuckets(name, help string, bucketsUs []int64) *Histogram {
	buckets := make([]float64, len(bucketsUs))
	for i, b := range bucketsUs {
		buckets[i] = float64(b) / 1_000_000
	}
	h := &Histogram{
		buckets: bucketsUs, bucketVals: make([]atomic.Int64, len(bucketsUs)),
		ph: prometheus.NewHistogram(prometheus.HistogramOpts{Name: name, Help: help, Buckets: buckets}),
	}
	_ = prometheus.Register(h.ph)
	return h
}

func (h *Histogram) Observe(seconds float64) {
	us := int64(seconds * 1_000_000)
	h.count.Add(1)
	h.sumUs.Add(us)
	for i, bound := range h.buckets {
		if us <= bound {
			h.bucketVals[i].Add(1)
		}
	}
	if h.ph != nil {
		h.ph.Observe(seconds)
	}
}

func (h *Histogram) Value() (count, sumUs int64) {
	return h.count.Load(), h.sumUs.Load()
}

func (h *Histogram) BucketSnapshot() (bounds []int64, counts []int64) {
	bounds = make([]int64, len(h.buckets))
	counts = make([]int64, len(h.buckets))
	for i := range h.buckets {
		bounds[i] = h.buckets[i]
		counts[i] = h.bucketVals[i].Load()
	}
	return
}

var (
	HTTPRequests = NewCounterVec(
		"http_requests_total",
		"Total number of HTTP requests.",
		[]string{"method", "route", "status"},
	)
	HTTPDuration = NewCounterVec(
		"http_request_duration_microseconds",
		"HTTP request latency in microseconds.",
		[]string{"method", "route"},
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
	FanoutHistogram = NewHistogramWithBuckets(
		"fanout_duration_seconds",
		"Fanout write diffusion latency in seconds.",
		[]int64{10_000, 50_000, 100_000, 500_000, 1_000_000, 5_000_000},
	)
	FeedMergeHistogram = NewHistogramWithBuckets(
		"feed_merge_duration_seconds",
		"Following feed merge latency in seconds.",
		[]int64{1000, 5000, 10_000, 50_000, 100_000, 500_000},
	)
	DBErrors = NewCounterVec(
		"db_errors_total",
		"Total number of database errors.",
		[]string{"operation"},
	)
)

func RecordLatency(cv *CounterVec, method, route string, dur time.Duration) {
	switch len(cv.labels) {
	case 0:
		cv.WithLabelValues().Add(dur.Microseconds())
	case 1:
		label := route
		if label == "" {
			label = method
		}
		if label == "" {
			label = "latency"
		}
		cv.WithLabelValues(label).Add(dur.Microseconds())
	default:
		cv.WithLabelValues(method, route).Add(dur.Microseconds())
	}
}

func Handler() http.Handler { return promhttp.Handler() }

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		route := routeFromRequest(r)
		status := strconv.Itoa(sw.status)
		HTTPRequests.WithLabelValues(r.Method, route, status).Inc()
		RecordLatency(HTTPDuration, r.Method, route, time.Since(start))
	})
}

func GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = routeFromRequest(c.Request)
		}
		status := strconv.Itoa(c.Writer.Status())
		HTTPRequests.WithLabelValues(c.Request.Method, route, status).Inc()
		RecordLatency(HTTPDuration, c.Request.Method, route, time.Since(start))
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

func routeFromRequest(r *http.Request) string {
	if r == nil || r.URL == nil {
		return "unknown"
	}
	path := r.URL.Path
	if path == "" {
		return "/"
	}
	return strings.TrimRight(path, "/")
}

func joinLabels(vals []string) string {
	if len(vals) == 0 {
		return ""
	}
	return strings.Join(vals, ",")
}

func normalizeLabels(vals []string, want int) []string {
	if want <= 0 {
		return nil
	}
	out := make([]string, want)
	copy(out, vals)
	for i := range out {
		if out[i] == "" {
			out[i] = "unknown"
		}
	}
	return out
}
