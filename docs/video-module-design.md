# 视频功能模块设计文档

> 版本: v1.0  
> 日期: 2026-06-05  
> 目标: 实现类抖音的完整视频上传→转码→分发→消费链路

---

## 1. 现状分析

### 已实现

| 功能 | 文件 | 说明 |
|------|------|------|
| 视频上传 | `api/upload.go` | multipart 表单, 100MB 限制, 格式校验, 本地存储 |
| 视频发布 | `api/video.go:PublishVideo` | title/play_url/cover_url 写入 store |
| 静态服务 | `router.go:r.Static` | Gin 本地文件服务 |
| 广场流 | `api/video.go:ListVideos` | 按时间倒序, 游标分页 |

### 缺失

| 功能 | 影响 |
|------|------|
| **转码管线** | 用户上传 4K→无法播放; 弱网无法自适应码率 |
| **封面自动生成** | cover_url 为空, 用户体验差 |
| **视频时长** | model.Video 无 Duration 字段 |
| **转码状态** | 无, 无法告知用户"视频处理中" |
| **CDN/OSS** | 本地文件服务不可扩展 |
| **多分辨率** | 无 360p/720p/1080p 自适应 |

---

## 2. 整体架构

```
用户上传
    │
    ▼
┌─────────────┐     ┌──────────────┐     ┌─────────────┐
│  API Gateway │────▶│ 视频服务      │────▶│ OSS/MinIO   │
│  (Gin)       │     │ (校验+存储)   │     │ (原始文件)   │
└─────────────┘     └──────┬───────┘     └─────────────┘
                           │ MQ (fanout task)
                           ▼
                    ┌──────────────┐
                    │ 转码 Worker   │
                    │ (ffmpeg)     │
                    ├──────────────┤
                    │ 360p  → OSS  │
                    │ 720p  → OSS  │
                    │ 1080p → OSS  │
                    │ 封面  → OSS  │
                    │ 水印  → OSS  │
                    └──────┬───────┘
                           │ 更新 DB status
                           ▼
                    ┌──────────────┐
                    │ CDN 刷新      │
                    │ (阿里云/腾讯) │
                    └──────────────┘
```

## 3. 数据模型

### 3.1 Video 表 (MySQL)

```sql
CREATE TABLE video (
    video_id     BIGINT PRIMARY KEY,
    author_id    BIGINT NOT NULL,
    title        VARCHAR(255) NOT NULL DEFAULT '',
    duration     INT NOT NULL DEFAULT 0 COMMENT '视频时长(秒)',
    -- 多分辨率播放地址 (JSON)
    play_urls    JSON COMMENT '{"360p":"url","720p":"url","1080p":"url"}',
    cover_url    VARCHAR(512) NOT NULL DEFAULT '',
    -- 转码状态
    status       TINYINT NOT NULL DEFAULT 0 COMMENT '0=上传中 1=转码中 2=已完成 3=失败',
    -- 文件信息
    file_size    BIGINT NOT NULL DEFAULT 0 COMMENT '原始文件大小(字节)',
    width        INT NOT NULL DEFAULT 0,
    height       INT NOT NULL DEFAULT 0,
    -- 互动统计 (Redis 真源，MySQL 快照)
    like_count    BIGINT NOT NULL DEFAULT 0,
    comment_count BIGINT NOT NULL DEFAULT 0,
    share_count   BIGINT NOT NULL DEFAULT 0,
    view_count    BIGINT NOT NULL DEFAULT 0,
    -- 时间戳
    created_at   BIGINT NOT NULL COMMENT '毫秒',
    updated_at   BIGINT NOT NULL COMMENT '毫秒',
    INDEX idx_author (author_id, created_at),
    INDEX idx_status (status, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 3.2 Go Model

```go
type VideoStatus int

const (
    VideoUploading   VideoStatus = 0 // 上传中
    VideoTranscoding VideoStatus = 1 // 转码中
    VideoReady       VideoStatus = 2 // 已完成
    VideoFailed      VideoStatus = 3 // 失败
)

type Video struct {
    ID           int64           `json:"id" db:"video_id"`
    AuthorID     int64           `json:"author_id" db:"author_id"`
    Title        string          `json:"title" db:"title"`
    Duration     int             `json:"duration" db:"duration"`
    PlayURLs     json.RawMessage `json:"play_urls" db:"play_urls"`   // {"360p":"...","720p":"...","1080p":"..."}
    CoverURL     string          `json:"cover_url" db:"cover_url"`
    Status       VideoStatus     `json:"status" db:"status"`
    FileSize     int64           `json:"file_size" db:"file_size"`
    Width        int             `json:"width" db:"width"`
    Height       int             `json:"height" db:"height"`
    LikeCount    int64           `json:"like_count" db:"like_count"`
    CommentCount int64           `json:"comment_count" db:"comment_count"`
    ViewCount    int64           `json:"view_count" db:"view_count"`
    CreatedAt    int64           `json:"created_at" db:"created_at"`
    UpdatedAt    int64           `json:"updated_at" db:"updated_at"`
}
```

### 3.3 Redis 缓存

| Key | 类型 | 说明 |
|-----|------|------|
| `video:meta:{vid}` | HASH | 视频元信息缓存 (TTL 1h) |
| `video:play_urls:{vid}` | HASH | 多分辨率播放地址 |
| `video:views:{vid}` | STRING | 播放计数 (INCR) |

---

## 4. 转码规格

| 分辨率 | 码率 | 用途 |
|--------|------|------|
| 360p (640×360) | 500kbps | 弱网/省流模式 |
| 540p (960×540) | 1000kbps | 默认清晰度 |
| 720p (1280×720) | 2000kbps | 高清 |
| 1080p (1920×1080) | 4000kbps | 超清 (原画>1080p 保留) |

### ffmpeg 命令

```bash
# 360p
ffmpeg -i input.mp4 -vf scale=640:360 -c:v libx264 -b:v 500k \
  -c:a aac -b:a 64k -preset fast -movflags +faststart output_360p.mp4

# 720p  
ffmpeg -i input.mp4 -vf scale=1280:720 -c:v libx264 -b:v 2000k \
  -c:a aac -b:a 128k -preset fast -movflags +faststart output_720p.mp4

# 封面 (第1秒截帧)
ffmpeg -i input.mp4 -ss 00:00:01 -vframes 1 -q:v 2 cover.jpg

# 获取视频信息
ffprobe -v quiet -print_format json -show_format -show_streams input.mp4
```

---

## 5. API 设计

### 5.1 视频上传 (增强)

```
POST /api/upload
Content-Type: multipart/form-data

Request:
  file: <video_file>      (必需, max 500MB)
  title: "我的视频"        (可选, 默认取文件名)

Response:
{
  "code": 0,
  "data": {
    "video_id": 123,
    "status": 1,          // 1=转码中
    "play_url": "/uploads/raw/1717600000_abc123.mp4",
    "filename": "my_video.mp4",
    "file_size": 52428800,
    "duration": 15,
    "width": 1920,
    "height": 1080
  }
}
```

### 5.2 转码状态查询

```
GET /api/videos/:id/status

Response:
{
  "code": 0,
  "data": {
    "video_id": 123,
    "status": 2,          // 0=上传中 1=转码中 2=已完成 3=失败
    "status_text": "已完成",
    "play_urls": {
      "360p":  "https://cdn.example.com/v/123_360p.mp4",
      "540p":  "https://cdn.example.com/v/123_540p.mp4",
      "720p":  "https://cdn.example.com/v/123_720p.mp4",
      "1080p": "https://cdn.example.com/v/123_1080p.mp4"
    },
    "cover_url": "https://cdn.example.com/v/123_cover.jpg",
    "progress": 100       // 转码进度 0-100
  }
}
```

### 5.3 视频 Feed (增强)

```
GET /api/videos?max_id=0&limit=10

Response:
{
  "code": 0,
  "data": {
    "items": [{
      "id": 123,
      "author": { "id": 1, "username": "alice" },
      "title": "我的视频",
      "duration": 15,
      "cover_url": "https://cdn.example.com/v/123_cover.jpg",
      "play_urls": {
        "360p": "...", "720p": "...", "1080p": "..."
      },
      "like_count": 1024,
      "comment_count": 88,
      "view_count": 50000,
      "liked": true,
      "created_at": 1717600000000
    }],
    "next_cursor": 100
  }
}
```

---

## 6. 转码 Worker 设计

### 6.1 文件: `cmd/transcode/main.go`

```
消费 MQ 转码任务:
  1. 从 OSS 下载原始文件
  2. ffprobe 获取视频信息 (duration/width/height)
  3. 更新 DB status=1 (转码中)
  4. 并发转码: 360p / 540p / 720p / 1080p + 封面
  5. 上传到 OSS + CDN 刷新
  6. 更新 DB status=2 (已完成) + play_urls
  7. 写入 Redis 缓存
```

### 6.2 转码任务结构

```go
type TranscodeTask struct {
    VideoID   int64  `json:"video_id"`
    AuthorID  int64  `json:"author_id"`
    SourceURL string `json:"source_url"` // OSS原始文件地址
    TsMilli   int64  `json:"ts_milli"`
}
```

---

## 7. 文件存储方案

### 开发环境

```
data/uploads/
├── raw/      原始上传文件
├── 360p/     转码输出
├── 720p/
├── 1080p/
└── covers/   封面图片
```

### 生产环境

```
OSS Bucket: shortvideo-videos
├── raw/{video_id}.mp4
├── {video_id}_360p.mp4
├── {video_id}_720p.mp4
├── {video_id}_1080p.mp4
└── covers/{video_id}.jpg

CDN Domain: cdn.shortvideo.com
```

---

## 8. 实施计划

| 阶段 | 内容 | 文件 |
|------|------|------|
| **Phase 1** | 增强 Video Model (+Duration/Status/PlayURLs) | `model/model.go` |
| | MySQL video 表升级 (已建, 加字段) | `mysqlx/migrate.go` |
| | 更新 store.CreateVideo | `store/store.go` |
| **Phase 2** | ffprobe 视频信息提取 | `pkg/media/probe.go` |
| | 上传时自动提取 Duration/Width/Height | `api/upload.go` |
| **Phase 3** | 转码 Worker (ffmpeg) | `cmd/transcode/main.go` |
| | 多分辨率输出 + 封面生成 | `internal/transcode/` |
| **Phase 4** | OSS 上传 + CDN 刷新 | `pkg/oss/` |
| | API: 转码状态查询 | `api/video.go` |
| **Phase 5** | 播放计数 (Redis INCR) | `api/video.go` |
| | Feed 响应增强 (author info 嵌入) | `api/video.go` |

---

## 9. 验证清单

- [ ] 上传视频 → 自动提取 Duration/Width/Height
- [ ] 转码 Worker 消费任务 → 输出 360p/720p/1080p
- [ ] 封面自动生成 (第1秒截帧)
- [ ] `GET /api/videos/:id/status` 返回转码进度
- [ ] 转码完成后 Feed 返回多分辨率 play_urls
- [ ] 视频详情含 duration/cover/play_urls
- [ ] 广场流过滤掉 status != 2 的视频
