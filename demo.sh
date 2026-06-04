#!/usr/bin/env bash
# 短视频后端接口演示脚本。先启动服务:go run ./cmd/server,再执行 bash demo.sh
set -e
BASE="${BASE:-http://localhost:8080}"

# 若安装了 jq 则美化输出,否则原样打印
pp() { if command -v jq >/dev/null 2>&1; then jq .; else cat; fi; }

echo "== 健康检查 =="
curl -s "$BASE/healthz" | pp

echo; echo "== 创建用户 david =="
curl -s -X POST "$BASE/api/users" -d '{"username":"david"}' | pp

echo; echo "== alice(1) 发布视频 =="
curl -s -X POST "$BASE/api/videos" \
  -H 'X-User-Id: 1' \
  -d '{"title":"我的第一条视频","play_url":"/uploads/demo.mp4"}' | pp

echo; echo "== 广场流(carol 视角,带 liked) =="
curl -s "$BASE/api/videos?limit=5" -H 'X-User-Id: 3' | pp

echo; echo "== carol(3) 给视频 1 点赞 =="
curl -s -X POST "$BASE/api/videos/1/like" -H 'X-User-Id: 3' | pp

echo; echo "== 重复点赞(幂等,changed=false) =="
curl -s -X POST "$BASE/api/videos/1/like" -H 'X-User-Id: 3' | pp

echo; echo "== 取消点赞 =="
curl -s -X DELETE "$BASE/api/videos/1/like" -H 'X-User-Id: 3' | pp

echo; echo "== carol 发表评论 =="
curl -s -X POST "$BASE/api/videos/1/comments" \
  -H 'X-User-Id: 3' -d '{"content":"拍得真好!"}' | pp

echo; echo "== 视频 1 的评论列表 =="
curl -s "$BASE/api/videos/1/comments" | pp

echo; echo "== carol 的关注流(已预置关注 alice、bob) =="
curl -s "$BASE/api/feed?limit=10" -H 'X-User-Id: 3' | pp

echo; echo "演示完成。"
