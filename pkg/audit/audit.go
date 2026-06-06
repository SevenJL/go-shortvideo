// Package audit provides structured request and user-action logging.
package audit

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

const RequestIDKey = "request_id"

var logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))

// Configure installs a process-wide structured logger.
func Configure(serviceName string) {
	if serviceName == "" {
		serviceName = "shortvideo"
	}
	logger = slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", serviceName)
	slog.SetDefault(logger)
}

// RequestID ensures every request has an id and exposes it to handlers/logs.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-Id")
		if rid == "" {
			rid = uuid.NewString()
		}
		c.Set(RequestIDKey, rid)
		c.Header("X-Request-Id", rid)
		c.Next()
	}
}

// GinMiddleware writes structured access logs and audit records for mutating
// business endpoints.
func GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		uid := userID(c)
		attrs := []slog.Attr{
			slog.String("event", "http_request"),
			slog.String("request_id", requestID(c)),
			slog.String("trace_id", traceID(c.Request.Context())),
			slog.String("method", c.Request.Method),
			slog.String("route", route),
			slog.Int("status", c.Writer.Status()),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("ip", c.ClientIP()),
			slog.Int64("user_id", uid),
		}
		logger.LogAttrs(c.Request.Context(), logLevel(c.Writer.Status()), "http request", attrs...)

		if shouldAudit(c.Request.Method, route) {
			attrs[0] = slog.String("event", "audit_user_action")
			logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "audit user action", attrs...)
		}
	}
}

func requestID(c *gin.Context) string {
	if v, ok := c.Get(RequestIDKey); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func traceID(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	sc := span.SpanContext()
	if !sc.HasTraceID() {
		return ""
	}
	return sc.TraceID().String()
}

func userID(c *gin.Context) int64 {
	if uid, ok := c.Get("user_id"); ok {
		switch v := uid.(type) {
		case int64:
			return v
		case int:
			return int64(v)
		case string:
			n, _ := strconv.ParseInt(v, 10, 64)
			return n
		}
	}
	return 0
}

func shouldAudit(method, route string) bool {
	if method != http.MethodPost && method != http.MethodPut &&
		method != http.MethodPatch && method != http.MethodDelete {
		return false
	}
	return route == "/api/login" ||
		route == "/api/users" ||
		route == "/api/upload" ||
		strings.HasPrefix(route, "/api/videos") ||
		strings.HasPrefix(route, "/api/users/")
}

func logLevel(status int) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status >= 400:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}
