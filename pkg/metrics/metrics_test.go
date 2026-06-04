package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCounter_Inc(t *testing.T) {
	c := NewCounter("test_total", "Test counter.")
	if c.Value() != 0 {
		t.Fatal("initial value should be 0")
	}
	c.Inc()
	c.Inc()
	if c.Value() != 2 {
		t.Fatalf("want 2, got %d", c.Value())
	}
	c.Add(5)
	if c.Value() != 7 {
		t.Fatalf("want 7, got %d", c.Value())
	}
}

func TestCounterVec_WithLabelValues(t *testing.T) {
	cv := NewCounterVec("requests_total", "Requests.", []string{"method", "status"})

	cv.WithLabelValues("GET", "200").Inc()
	cv.WithLabelValues("GET", "200").Inc()
	cv.WithLabelValues("POST", "201").Inc()

	snap := cv.snapshot()
	if len(snap) != 2 {
		t.Fatalf("want 2 label combos, got %d", len(snap))
	}
}

func TestCounterVec_SameLabels(t *testing.T) {
	cv := NewCounterVec("x_total", "X.", []string{"a"})

	a := cv.WithLabelValues("1")
	b := cv.WithLabelValues("1")
	if a != b {
		t.Fatal("same labels should return same counter")
	}
}

func TestHandler(t *testing.T) {
	// 创建一些指标
	NewCounter("test_metric", "A test metric.").Inc()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if body == "" {
		t.Fatal("metrics body should not be empty")
	}
	if len(body) < 50 {
		t.Fatalf("metrics output too short: %q", body)
	}
}

func TestMiddleware(t *testing.T) {
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	// 验证指标已记录
	snap := HTTPRequests.snapshot()
	found := false
	for k, v := range snap {
		if v > 0 {
			found = true
			_ = k
			break
		}
	}
	if !found {
		t.Fatal("HTTPRequests should have recorded data")
	}
}

func TestMiddleware_ErrorStatus(t *testing.T) {
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	req := httptest.NewRequest(http.MethodGet, "/notfound", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestLikeOps_Counter(t *testing.T) {
	LikeOps.WithLabelValues("like", "redis").Inc()
	LikeOps.WithLabelValues("like", "mem").Inc()
	LikeOps.WithLabelValues("unlike", "redis").Inc()

	snap := LikeOps.snapshot()
	if len(snap) < 2 {
		t.Fatalf("want at least 2 entries, got %d", len(snap))
	}
}

func TestDBErrors_Counter(t *testing.T) {
	DBErrors.WithLabelValues("upsert").Inc()
	DBErrors.WithLabelValues("upsert").Inc()
	DBErrors.WithLabelValues("count_delta").Inc()

	snap := DBErrors.snapshot()
	if len(snap) != 2 {
		t.Fatalf("want 2, got %d", len(snap))
	}
}

func TestRecordLatency(t *testing.T) {
	RecordLatency(FanoutOps, "", "", 1_000_000)
	RecordLatency(FanoutOps, "", "", 500_000)

	snap := FanoutOps.snapshot()
	if len(snap) == 0 {
		t.Fatal("FanoutOps should have recorded data")
	}
}

func TestFanoutHistogram(t *testing.T) {
	FanoutHistogram.Observe(0.05)  // 50ms
	FanoutHistogram.Observe(0.5)   // 500ms
	FanoutHistogram.Observe(0.001) // 1ms

	count, sumUs := FanoutHistogram.Value()
	if count != 3 {
		t.Fatalf("want count=3, got %d", count)
	}
	_ = sumUs

	bounds, buckCounts := FanoutHistogram.BucketSnapshot()
	if len(bounds) != 6 {
		t.Fatalf("want 6 buckets, got %d", len(bounds))
	}
	// 1ms < 10ms, should be in first bucket
	if buckCounts[0] < 1 {
		t.Fatal("1ms observation should be in first bucket")
	}
	// 50ms > 10ms, <= 50ms → second bucket
	if buckCounts[1] < 1 {
		t.Fatal("50ms observation should be in second bucket")
	}
}

func TestFeedMergeHistogram(t *testing.T) {
	FeedMergeHistogram.Observe(0.001) // 1ms
	FeedMergeHistogram.Observe(0.300) // 300ms

	count, _ := FeedMergeHistogram.Value()
	if count != 2 {
		t.Fatalf("want count=2, got %d", count)
	}
}

func TestJoinLabels(t *testing.T) {
	if joinLabels(nil) != "" {
		t.Fatal("nil should return empty string")
	}
	if joinLabels([]string{}) != "" {
		t.Fatal("empty should return empty string")
	}
	if joinLabels([]string{"a"}) != "a" {
		t.Fatal("single label")
	}
	if joinLabels([]string{"a", "b", "c"}) != "a,b,c" {
		t.Fatal("multiple labels")
	}
}
