// cmd/like 是点赞服务入口，提供幂等点赞/取消接口，并在后台运行计数聚合器。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"shortvideo/internal/like"
	"shortvideo/pkg/mq"
	"shortvideo/pkg/redisx"
)

func main() {
	addr := flag.String("addr", ":8082", "HTTP 监听地址")
	redisAddr := flag.String("redis", envOrDefault("REDIS_ADDR", "localhost:6379"), "Redis 地址")
	flag.Parse()

	rdb := redisx.NewClient(*redisAddr)

	// MQ：内存 bus（生产替换为 Kafka Producer）
	bus := mq.NewChanBus(1024)
	producer := &busProducer{bus: bus, topic: "like-events"}

	likeSvc := like.NewService(rdb, producer)

	// 本地聚合器：每 100ms 批量刷 Redis（可按需调整 interval）
	aggregator := like.NewCounterAggregator(rdb)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go aggregator.Run(ctx, 100*time.Millisecond)

	// --- HTTP 路由 ---
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/v2/videos/{id}/like", func(w http.ResponseWriter, r *http.Request) {
		uid, vid, ok := parseIDs(w, r)
		if !ok {
			return
		}
		changed, err := likeSvc.Like(ctx, uid, vid)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		cnt, _ := likeSvc.Count(ctx, vid)
		writeOK(w, map[string]interface{}{"changed": changed, "liked": true, "like_count": cnt})
	})

	mux.HandleFunc("DELETE /api/v2/videos/{id}/like", func(w http.ResponseWriter, r *http.Request) {
		uid, vid, ok := parseIDs(w, r)
		if !ok {
			return
		}
		changed, err := likeSvc.Unlike(ctx, uid, vid)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		cnt, _ := likeSvc.Count(ctx, vid)
		writeOK(w, map[string]interface{}{"changed": changed, "liked": false, "like_count": cnt})
	})

	mux.HandleFunc("GET /api/v2/videos/{id}/like/count", func(w http.ResponseWriter, r *http.Request) {
		vidStr := r.PathValue("id")
		vid, err := strconv.ParseInt(vidStr, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "视频 id 非法")
			return
		}
		cnt, err := likeSvc.Count(ctx, vid)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeOK(w, map[string]interface{}{"video_id": vid, "like_count": cnt})
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, map[string]string{"status": "ok", "service": "like"})
	})

	srv := &http.Server{Addr: *addr, Handler: mux, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	go func() {
		log.Printf("点赞服务启动，监听 %s", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}

func parseIDs(w http.ResponseWriter, r *http.Request) (uid, vid int64, ok bool) {
	uidStr := r.Header.Get("X-User-Id")
	uid, err := strconv.ParseInt(uidStr, 10, 64)
	if err != nil || uid <= 0 {
		writeErr(w, http.StatusUnauthorized, "缺少或非法的 X-User-Id")
		return 0, 0, false
	}
	vid, err = strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "视频 id 非法")
		return 0, 0, false
	}
	return uid, vid, true
}

// busProducer 将 mq.ChanBus 适配为 like.EventProducer。
type busProducer struct {
	bus   *mq.ChanBus
	topic string
}

func (p *busProducer) Publish(ctx context.Context, e like.LikeEvent) error {
	data, _ := json.Marshal(e)
	return p.bus.Publish(ctx, p.topic, data)
}

// --- HTTP 工具 ---

type resp struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data interface{} `json:"data,omitempty"`
}

func writeOK(w http.ResponseWriter, data interface{}) {
	writeJSON(w, http.StatusOK, resp{Code: 0, Msg: "ok", Data: data})
}
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, resp{Code: status, Msg: msg})
}
func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
