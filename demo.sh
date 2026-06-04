#!/usr/bin/env bash
# 短视频后端接口演示脚本。先启动服务:go run ./cmd/server,再执行 bash demo.sh
set -e
BASE="${BASE:-http://localhost:8080}"

# 若安装了 jq 则美化输出,否则原样打印
pp() { if command -v jq >/dev/null 2>&1; then jq .; else cat; fi; }

echo "== 健康检查 =="
curl -s "$BASE/healthz" | pp

echo; echo "== 注册用户 david(密码 david123) =="
curl -s -X POST "$BASE/api/users" \
  -H 'Content-Type: application/json' \
  -d '{"username":"david","password":"david123"}' | pp

echo; echo "== 登录 carol(预置用户,密码 password123)获取 JWT =="
TOKEN=$(curl -s -X POST "$BASE/api/login" \
  -H 'Content-Type: application/json' \
  -d '{"username":"carol","password":"password123"}' | \
  python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["token"])' 2>/dev/null || \
  echo "")
if [ -z "$TOKEN" ]; then
  echo "JWT 登录失败,回退到 X-User-Id 模式"
  AUTH_HEADER="X-User-Id: 3"
else
  echo "获取到 JWT: ${TOKEN:0:20}..."
  AUTH_HEADER="Authorization: Bearer $TOKEN"
fi

echo; echo "== alice(1) 发布视频 =="
curl -s -X POST "$BASE/api/videos" \
  -H 'X-User-Id: 1' \
  -H 'Content-Type: application/json' \
  -d '{"title":"我的第一条视频","play_url":"/uploads/demo.mp4"}' | pp

echo; echo "== 广场流(carol 视角,带 liked) =="
curl -s "$BASE/api/videos?limit=5" -H "$AUTH_HEADER" | pp

echo; echo "== carol 给视频 1 点赞 =="
curl -s -X POST "$BASE/api/videos/1/like" -H "$AUTH_HEADER" | pp

echo; echo "== 重复点赞(幂等,changed=false) =="
curl -s -X POST "$BASE/api/videos/1/like" -H "$AUTH_HEADER" | pp

echo; echo "== 取消点赞 =="
curl -s -X DELETE "$BASE/api/videos/1/like" -H "$AUTH_HEADER" | pp

echo; echo "== carol 发表评论 =="
curl -s -X POST "$BASE/api/videos/1/comments" \
  -H "$AUTH_HEADER" \
  -H 'Content-Type: application/json' \
  -d '{"content":"拍得真好!"}' | pp

echo; echo "== 视频 1 的评论列表 =="
curl -s "$BASE/api/videos/1/comments" | pp

echo; echo "== carol 的关注流(已预置关注 alice、bob) =="
curl -s "$BASE/api/feed?limit=10" -H "$AUTH_HEADER" | pp

echo; echo "演示完成。"
