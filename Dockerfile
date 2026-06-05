# ============================================
# Stage 1: Build
# ============================================
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /server ./cmd/server

# ============================================
# Stage 2: Runtime
# ============================================
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata curl && \
    cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime

# 可选: 安装 ffmpeg（如需转码功能）
# RUN apk add --no-cache ffmpeg

WORKDIR /app
COPY --from=builder /server .
COPY config.yaml .
COPY web/ ./web/

RUN mkdir -p /app/data/uploads

EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
  CMD curl -sf http://localhost:8080/healthz || exit 1

ENTRYPOINT ["./server", "-config", "config.yaml"]
