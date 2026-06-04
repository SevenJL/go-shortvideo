package model

// User 用户
type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`       // bcrypt hash, JSON 序列化时排除
	CreatedAt    int64  `json:"created_at"` // 毫秒时间戳
}

// Video 短视频元信息。真正的视频文件由 PlayURL 指向(本地 /uploads/xxx 或外部 CDN)。
type Video struct {
	ID           int64  `json:"id"`
	AuthorID     int64  `json:"author_id"`
	Title        string `json:"title"`
	PlayURL      string `json:"play_url"`
	CoverURL     string `json:"cover_url"`
	CreatedAt    int64  `json:"created_at"`
	LikeCount    int64  `json:"like_count"`
	CommentCount int64  `json:"comment_count"`
}

// Comment 评论
type Comment struct {
	ID        int64  `json:"id"`
	VideoID   int64  `json:"video_id"`
	UserID    int64  `json:"user_id"`
	Content   string `json:"content"`
	CreatedAt int64  `json:"created_at"`
}
