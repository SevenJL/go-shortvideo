.PHONY: run build demo tidy clean test lint docker-build docker-up docker-down release

# ---- 开发 ----

# 启动服务 (默认 :8080, 带演示数据)
run:
	go run ./cmd/server

# 编译
build:
	go build -ldflags="-s -w" -o bin/server ./cmd/server
	go build -ldflags="-s -w" -o bin/transcode ./cmd/transcode

# 运行接口演示
demo:
	bash demo.sh

# ---- 测试 ----

test:
	go test ./... -count=1 -timeout=2m

test-race:
	go test -race ./... -count=1 -timeout=5m

test-cover:
	go test ./... -count=1 -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html

test-integration:
	@echo "启动集成测试 (需 Redis + MySQL)..."
	go build -o bin/server ./cmd/server
	./bin/server -config config.yaml &
	sleep 3
	curl -sf http://localhost:8080/healthz && echo "OK" || echo "FAIL"
	curl -s -X POST http://localhost:8080/api/login \
		-H 'Content-Type: application/json' \
		-d '{"username":"alice","password":"password123"}' | python3 -m json.tool
	kill %1

# ---- 代码质量 ----

lint:
	golangci-lint run --timeout=5m

lint-fix:
	golangci-lint run --timeout=5m --fix

vet:
	go vet ./...

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

# ---- 安全 ----

security:
	go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

# ---- 构建 ----

build-all:
	GOOS=linux   GOARCH=amd64 go build -ldflags="-s -w" -o bin/shortvideo-linux-amd64   ./cmd/server
	GOOS=linux   GOARCH=arm64 go build -ldflags="-s -w" -o bin/shortvideo-linux-arm64   ./cmd/server
	GOOS=darwin  GOARCH=amd64 go build -ldflags="-s -w" -o bin/shortvideo-darwin-amd64  ./cmd/server
	GOOS=darwin  GOARCH=arm64 go build -ldflags="-s -w" -o bin/shortvideo-darwin-arm64  ./cmd/server

# ---- Docker ----

docker-build:
	docker build -t shortvideo:latest .

docker-build-transcode:
	docker build -f Dockerfile.transcode -t shortvideo-transcode:latest .

docker-up:
	docker compose up -d

docker-up-full:
	docker compose --profile full up -d

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f server

# ---- Release ----

release: build-all
	@echo "Release binaries: bin/"

# ---- 清理 ----

clean:
	rm -rf bin
	rm -f data/uploads/*.mp4 data/uploads/*.mov data/uploads/*.webm
	rm -f coverage.out coverage.html
