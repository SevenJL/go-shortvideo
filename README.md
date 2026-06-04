# 短视频后端(简单版 / Simple Short-Video Backend)

一个**零外部依赖**(仅用 Go 标准库)、**开箱即运行**的短视频后端示例。
实现了短视频应用的核心功能:用户、发布视频、视频流、点赞、评论、关注、关注流、视频上传。

> 配套设计文档(推拉结合 + 高并发点赞的工业级方案)见 `short-video-feed-like-design.md`。
> 本项目是其中思路的**最简可运行落地**:用内存存储代替 MySQL/Redis,便于先把功能跑通。

---

## ✨ 特性

- 纯 Go 标准库,**无需 `go get` 任何依赖**,有网络即可 `go run` 启动
- 线程安全的内存存储(读写锁保护)
- 点赞**按用户维度去重**(对应设计文档里消解热点的核心思路),幂等
- 游标分页的视频流(广场流 + 关注流)
- 视频文件上传 + 静态访问
- 优雅关闭、访问日志、统一 JSON 响应

---

## 🚀 快速开始

需要 **Go 1.22+**(用到标准库 `net/http` 的方法+路径路由)。

```bash
# 进入项目目录
cd shortvideo

# 方式一:直接运行
go run ./cmd/server

# 方式二:编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

启动后默认监听 `:8080`,并注入演示数据(用户 `alice`(id=1)、`bob`(id=2)、`carol`(id=3) 和 4 个视频)。

健康检查:

```bash
curl http://localhost:8080/healthz
```

可选参数:

```bash
go run ./cmd/server -addr :9000 -upload-dir ./data/uploads -seed=false
```

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-addr` | `:8080` | HTTP 监听地址 |
| `-upload-dir` | `./data/uploads` | 视频上传保存目录 |
| `-seed` | `true` | 是否注入演示数据 |

---

## 🔐 关于"当前用户"

为保持简单,本项目**不做真实鉴权**。需要登录态的接口通过请求头 `X-User-Id` 标识当前操作用户:

```
X-User-Id: 1
```

真实项目中应替换为 JWT / Session 等鉴权机制(改动只集中在 `internal/api/handler.go` 的 `currentUserID`)。

---

## 📚 API 文档

所有响应统一为:`{ "code": 0, "msg": "ok", "data": ... }`,出错时 `code` 为 HTTP 状态码。

| 方法 | 路径 | 说明 | 需要 `X-User-Id` |
|------|------|------|:---:|
| POST | `/api/users` | 创建用户 | 否 |
| GET | `/api/users/{id}` | 用户信息(含关注/粉丝数) | 否 |
| GET | `/api/users/{id}/videos` | 某用户发布的视频 | 可选* |
| POST | `/api/users/{id}/follow` | 关注该用户 | 是 |
| DELETE | `/api/users/{id}/follow` | 取消关注 | 是 |
| POST | `/api/videos` | 发布视频 | 是 |
| GET | `/api/videos` | 广场流(全部视频,倒序) | 可选* |
| GET | `/api/videos/{id}` | 视频详情 | 可选* |
| POST | `/api/videos/{id}/like` | 点赞 | 是 |
| DELETE | `/api/videos/{id}/like` | 取消点赞 | 是 |
| POST | `/api/videos/{id}/comments` | 发表评论 | 是 |
| GET | `/api/videos/{id}/comments` | 评论列表 | 否 |
| GET | `/api/feed` | 关注流(我关注的人发布的视频) | 是 |
| POST | `/api/upload` | 上传视频文件 | 否 |
| GET | `/uploads/{file}` | 访问已上传文件 | 否 |
| GET | `/healthz` | 健康检查 | 否 |

> \* 带 `X-User-Id` 时,返回的视频会附带 `liked` 字段(当前用户是否点赞)。

列表类接口支持游标分页:`?max_id=&limit=`。返回中的 `next_cursor` 即为下一页要传的 `max_id`(取更早的内容);`items` 为空表示到底了。

---

## 🧪 curl 示例

```bash
BASE=http://localhost:8080

# 1) 创建用户(返回 id)
curl -s -X POST $BASE/api/users -d '{"username":"david"}'

# 2) 以 alice(id=1)身份发布视频
curl -s -X POST $BASE/api/videos \
  -H 'X-User-Id: 1' \
  -d '{"title":"我的第一条视频","play_url":"/uploads/demo.mp4"}'

# 3) 广场流(带 X-User-Id 可看到 liked 字段)
curl -s "$BASE/api/videos?limit=5" -H 'X-User-Id: 3'

# 4) carol(id=3)给视频 1 点赞
curl -s -X POST $BASE/api/videos/1/like -H 'X-User-Id: 3'

# 5) 再点一次(幂等,changed=false,点赞数不变)
curl -s -X POST $BASE/api/videos/1/like -H 'X-User-Id: 3'

# 6) 取消点赞
curl -s -X DELETE $BASE/api/videos/1/like -H 'X-User-Id: 3'

# 7) 发表评论
curl -s -X POST $BASE/api/videos/1/comments \
  -H 'X-User-Id: 3' -d '{"content":"拍得真好!"}'

# 8) 评论列表
curl -s $BASE/api/videos/1/comments

# 9) carol 的关注流(已预置关注 alice、bob)
curl -s "$BASE/api/feed?limit=10" -H 'X-User-Id: 3'

# 10) 上传一个视频文件(把 path/to/video.mp4 换成真实文件)
curl -s -X POST $BASE/api/upload -F "file=@path/to/video.mp4"
# 返回的 play_url 可直接用于发布视频接口
```

一键演示脚本(需服务已启动):`bash demo.sh`

---

## 🗂 目录结构

```
shortvideo/
├── go.mod
├── README.md
├── demo.sh                      # 一键 curl 演示
├── cmd/server/main.go           # 程序入口
└── internal/
    ├── model/model.go           # 数据模型
    ├── store/store.go           # 内存存储(线程安全)
    └── api/
        ├── router.go            # 路由
        ├── handler.go           # 公共依赖与辅助函数、中间件
        ├── response.go          # 统一响应
        ├── user.go              # 用户接口
        ├── video.go             # 视频接口 + 信息流装配
        ├── like.go              # 点赞接口
        ├── comment.go           # 评论接口
        ├── follow.go            # 关注 + 关注流接口
        └── upload.go            # 视频上传
```

---

## 🔧 从"简单版"升级到生产架构

本项目刻意保持简单。按设计文档演进时:

1. **存储替换**:把 `internal/store` 换成 MySQL(关系/视频/评论)+ Redis(收件箱/计数/去重),方法签名不变,API 层零改动。
2. **关注流**:`FollowingFeed` 当前是"拉模型"的最简实现;高并发下改为 Redis 收件箱写扩散 + 大 V 读扩散合并。
3. **点赞**:把内存计数换成 Redis 分片计数器 + 本地聚合 + MQ 异步落库。
4. **鉴权**:`currentUserID` 换成 JWT / Session。
5. **视频**:上传后接入转码、对象存储与 CDN。

---

## ⚠️ 注意

- 内存存储,**进程重启数据全部丢失**,仅供学习与本地联调。
- 演示数据里的视频 `play_url` 是占位地址,直接播放会 404;请用 `POST /api/upload` 上传真实视频后再发布。
