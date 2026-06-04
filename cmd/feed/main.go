// cmd/feed 是关注流服务入口，提供推拉结合的 GET /api/v2/feed 接口。
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

	"shortvideo/internal/feed"
	"shortvideo/internal/like"
	"shortvideo/internal/model"
	"shortvideo/internal/relation"
	"shortvideo/internal/store"
	"shortvideo/pkg/redisx"
)

func main() {
	addr := flag.String("addr", ":8081", "HTTP 监听地址")
	redisAddr := flag.String("redis", envOrDefault("REDIS_ADDR", "localhost:6379"), "Redis 地址")
	flag.Parse()

	// --- 依赖初始化 ---
	rdb := redisx.NewClient(*redisAddr)
	memStore := store.New()
	memStore.Seed()

	relRepo := relation.NewMemRepo(memStore)
	relSvc := relation.NewService(rdb, relRepo)

	feedStore := feed.NewStore(rdb)

	// 点赞服务（空 producer：演示环境不需要真实 MQ）
	likeSvc := like.NewService(rdb, &noopProducer{})

	// VideoHydrator 适配器：将 store.Store 适配为 feed.VideoHydrator
	hydrator := &storeHydrator{s: memStore}

	feedSvc := feed.NewService(feedStore, relSvc, hydrator, likeSvc)

	// --- HTTP 路由 ---
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/feed", func(w http.ResponseWriter, r *http.Request) {
		uidStr := r.Header.Get("X-User-Id")
		uid, err := strconv.ParseInt(uidStr, 10, 64)
		if err != nil || uid <= 0 {
			writeErr(w, http.StatusUnauthorized, "缺少或非法的 X-User-Id")
			return
		}
		cursorStr := r.URL.Query().Get("cursor")
		var cursor float64
		if cursorStr != "" {
			cursor, _ = strconv.ParseFloat(cursorStr, 64)
		}
		page, err := feedSvc.GetFollowingFeed(r.Context(), uid, cursor)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeOK(w, map[string]interface{}{
			"items":       page.Videos,
			"next_cursor": page.NextCursor,
		})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, map[string]string{"status": "ok", "service": "feed"})
	})

	srv := &http.Server{Addr: *addr, Handler: mux, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	go func() {
		log.Printf("关注流服务启动，监听 %s", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// --- 适配器 ---

// storeHydrator 将内存 Store 适配为 feed.VideoHydrator。
type storeHydrator struct{ s *store.Store }

func (h *storeHydrator) BatchGetVisible(_ context.Context, ids []int64) ([]feed.VideoVO, error) {
	out := make([]feed.VideoVO, 0, len(ids))
	for _, id := range ids {
		v, err := h.s.GetVideo(id)
		if err != nil {
			continue // 跳过已删除或不存在的
		}
		out = append(out, toVO(v))
	}
	return out, nil
}

func toVO(v model.Video) feed.VideoVO {
	return feed.VideoVO{
		VideoID:   v.ID,
		AuthorID:  v.AuthorID,
		Title:     v.Title,
		PlayURL:   v.PlayURL,
		CoverURL:  v.CoverURL,
		CreatedAt: v.CreatedAt,
		LikeCount: v.LikeCount,
	}
}

// noopProducer 是开发环境的空 MQ 实现。
type noopProducer struct{}

func (p *noopProducer) Publish(_ context.Context, _ like.LikeEvent) error { return nil }

// --- HTTP 工具 ---

type resp struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data interface{} `json:"data,omitempty"`
}

func writeOK(w http.ResponseWriter, data interface{}) { writeJSON(w, http.StatusOK, resp{Code: 0, Msg: "ok", Data: data}) }
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
