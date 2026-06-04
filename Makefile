.PHONY: run build demo tidy clean

# 启动服务(默认 :8080,带演示数据)
run:
	go run ./cmd/server

# 编译到 bin/server
build:
	go build -o bin/server ./cmd/server

# 运行接口演示(需先在另一终端 make run)
demo:
	bash demo.sh

# 整理依赖(本项目零外部依赖,通常无变化)
tidy:
	go mod tidy

# 清理产物与上传数据
clean:
	rm -rf bin
	rm -f data/uploads/*.mp4 data/uploads/*.mov data/uploads/*.webm
