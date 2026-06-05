package model

// User 用户
type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`       // bcrypt hash, JSON 序列化时排除
	CreatedAt    int64  `json:"created_at"` // 毫秒时间戳
}

// VideoStatus 视频状态。
type VideoStatus int

const (
	VideoUploading   VideoStatus = 0 // 上传中
	VideoTranscoding VideoStatus = 1 // 转码中
	VideoReady       VideoStatus = 2 // 已完成
	VideoFailed      VideoStatus = 3 // 失败
)

// Video 短视频元信息。
type Video struct {
	ID           int64       `json:"id"`
	AuthorID     int64       `json:"author_id"`
	Title        string      `json:"title"`
	PlayURL      string      `json:"play_url"`
	CoverURL     string      `json:"cover_url"`
	Duration     int         `json:"duration"`      // 时长(秒)
	Status       VideoStatus `json:"status"`         // 转码状态
	Width        int         `json:"width"`
	Height       int         `json:"height"`
	FileSize     int64       `json:"file_size"`
	CreatedAt    int64       `json:"created_at"`
	LikeCount    int64       `json:"like_count"`
	CommentCount int64       `json:"comment_count"`
	ViewCount    int64       `json:"view_count"`
}

// Comment 评论
type Comment struct {
	ID        int64  `json:"id"`
	VideoID   int64  `json:"video_id"`
	UserID    int64  `json:"user_id"`
	Content   string `json:"content"`
	CreatedAt int64  `json:"created_at"`
}
