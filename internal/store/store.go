package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"shortvideo/internal/model"
)

// 业务错误
var (
	ErrNotFound      = errors.New("资源不存在")
	ErrInvalid       = errors.New("参数非法")
	ErrWrongPassword = errors.New("密码错误")
)

// Store 是线程安全的内存存储(演示用,进程重启后数据丢失)。
// 生产环境可把这一层替换为 MySQL + Redis,只要保持方法签名不变,
// 上层 API 代码无需改动。
type Store struct {
	mu sync.RWMutex
	db *sql.DB

	users      map[int64]*model.User
	userByName map[string]*model.User // username → user, O(1) lookup
	videos     map[int64]*model.Video
	comments   map[int64]*model.Comment

	// 点赞按"用户维度"存储:userID -> set(videoID)。
	// 这样既能 O(1) 判断"我是否点过赞",又天然去重;
	// 且热门视频的点赞请求会分散到不同用户的 key 上,不形成单点热点
	// (这正是设计文档里"去重按用户维度"的思路)。
	userLikes map[int64]map[int64]struct{}

	// 关注关系(双向索引,空间换查询效率)
	following map[int64]map[int64]struct{} // followerID -> set(followeeID):我关注了谁
	followers map[int64]map[int64]struct{} // followeeID -> set(followerID):谁关注了我

	// 视频 -> 评论 ID 列表(按发表顺序)
	videoComments map[int64][]int64

	// 自增 ID(均在写锁内递增)
	userSeq    int64
	videoSeq   int64
	commentSeq int64
}

// New 创建一个空存储。
func New() *Store {
	return &Store{
		users:         make(map[int64]*model.User),
		userByName:    make(map[string]*model.User),
		videos:        make(map[int64]*model.Video),
		comments:      make(map[int64]*model.Comment),
		userLikes:     make(map[int64]map[int64]struct{}),
		following:     make(map[int64]map[int64]struct{}),
		followers:     make(map[int64]map[int64]struct{}),
		videoComments: make(map[int64][]int64),
	}
}

// NewMySQL 创建一个以 MySQL 为生产真源的 Store。未覆盖的测试/开发路径仍可使用 New。
func NewMySQL(db *sql.DB) *Store {
	s := New()
	s.db = db
	return s
}

func nowMilli() int64 { return time.Now().UnixMilli() }

// ---------------- 用户 ----------------

// CreateUser 创建用户。password 会经 bcrypt 哈希后存储。
func (s *Store) CreateUser(username, password string) (*model.User, error) {
	if username == "" || password == "" {
		return nil, ErrInvalid
	}
	if len(password) < 6 {
		return nil, errors.New("密码长度不能少于 6 位")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	if s.db != nil {
		ts := nowMilli()
		res, err := s.db.ExecContext(context.Background(),
			`INSERT INTO user_account (username, password_hash, created_at) VALUES (?, ?, ?)`,
			username, string(hash), ts,
		)
		if err != nil {
			return nil, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, err
		}
		return &model.User{ID: id, Username: username, PasswordHash: string(hash), CreatedAt: ts}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userSeq++
	u := &model.User{ID: s.userSeq, Username: username, PasswordHash: string(hash), CreatedAt: nowMilli()}
	s.users[u.ID] = u
	s.userByName[username] = u
	return u, nil
}

// GetUserByUsername 通过用户名查找用户（O(1) 索引，用于登录）。
func (s *Store) GetUserByUsername(username string) (*model.User, error) {
	if s.db != nil {
		var u model.User
		err := s.db.QueryRowContext(context.Background(),
			`SELECT user_id, username, password_hash, created_at FROM user_account WHERE username = ?`,
			username,
		).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt)
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		return &u, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.userByName[username]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *u
	return &cp, nil
}

// AuthenticateUser 验证用户名和密码，成功返回用户。
func (s *Store) AuthenticateUser(username, password string) (*model.User, error) {
	u, err := s.GetUserByUsername(username)
	if err != nil {
		return nil, ErrNotFound
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, ErrWrongPassword
	}
	// 返回时抹去密码哈希
	u.PasswordHash = ""
	return u, nil
}

// GetUser 查询用户。
func (s *Store) GetUser(id int64) (*model.User, error) {
	if s.db != nil {
		var u model.User
		err := s.db.QueryRowContext(context.Background(),
			`SELECT user_id, username, password_hash, created_at FROM user_account WHERE user_id = ?`,
			id,
		).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt)
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		return &u, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *u
	return &cp, nil
}

// FollowStats 返回某用户的关注数与粉丝数。
func (s *Store) FollowStats(userID int64) (followingCount int, followerCount int) {
	if s.db != nil {
		var following, followers int
		_ = s.db.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM following WHERE user_id = ?`, userID,
		).Scan(&following)
		_ = s.db.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM follower WHERE followee_id = ?`, userID,
		).Scan(&followers)
		return following, followers
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.following[userID]), len(s.followers[userID])
}

// ---------------- 视频 ----------------

// CreateVideo 发布视频。新增 Duration/Width/Height/FileSize/Status 字段。
func (s *Store) CreateVideo(authorID int64, title, playURL, coverURL string, duration int, width, height int, fileSize int64) (model.Video, error) {
	if title == "" || playURL == "" {
		return model.Video{}, ErrInvalid
	}
	if s.db != nil {
		if _, err := s.GetUser(authorID); err != nil {
			return model.Video{}, err
		}
		ts := nowMilli()
		status := model.VideoReady
		res, err := s.db.ExecContext(context.Background(),
			`INSERT INTO video (author_id, title, play_url, cover_url, duration, width, height, file_size, status, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			authorID, title, playURL, coverURL, duration, width, height, fileSize, status, ts, ts,
		)
		if err != nil {
			return model.Video{}, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return model.Video{}, err
		}
		return model.Video{
			ID: id, AuthorID: authorID, Title: title, PlayURL: playURL, CoverURL: coverURL,
			Duration: duration, Status: status, Width: width, Height: height, FileSize: fileSize,
			CreatedAt: ts,
		}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[authorID]; !ok {
		return model.Video{}, ErrNotFound
	}
	s.videoSeq++
	status := model.VideoReady // 无转码时直接完成
	v := &model.Video{
		ID:        s.videoSeq,
		AuthorID:  authorID,
		Title:     title,
		PlayURL:   playURL,
		CoverURL:  coverURL,
		Duration:  duration,
		Status:    status,
		Width:     width,
		Height:    height,
		FileSize:  fileSize,
		CreatedAt: nowMilli(),
	}
	s.videos[v.ID] = v
	return *v, nil
}

// GetVideo 查询单个视频(返回副本,避免与计数自增产生数据竞争)。
func (s *Store) GetVideo(id int64) (model.Video, error) {
	if s.db != nil {
		var v model.Video
		var status int
		var likeCount, commentCount int64
		var playURLsJSON string
		err := s.db.QueryRowContext(context.Background(),
			`SELECT v.video_id, v.author_id, v.title, v.play_url, COALESCE(v.play_urls, ''), v.cover_url, v.duration, v.width, v.height,
			        v.file_size, v.status, v.created_at, COALESCE(st.like_count, 0), COALESCE(st.comment_count, 0)
			   FROM video v
			   LEFT JOIN video_stats st ON st.video_id = v.video_id
			  WHERE v.video_id = ?`,
			id,
		).Scan(&v.ID, &v.AuthorID, &v.Title, &v.PlayURL, &playURLsJSON, &v.CoverURL, &v.Duration, &v.Width, &v.Height,
			&v.FileSize, &status, &v.CreatedAt, &likeCount, &commentCount)
		if err == sql.ErrNoRows {
			return model.Video{}, ErrNotFound
		}
		if err != nil {
			return model.Video{}, err
		}
		v.Status = model.VideoStatus(status)
		v.PlayURLs = decodePlayURLs(playURLsJSON)
		v.LikeCount = likeCount
		v.CommentCount = commentCount
		return v, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.videos[id]
	if !ok {
		return model.Video{}, ErrNotFound
	}
	return *v, nil
}

// UpdateVideoStatus 更新视频状态，供转码 Worker 回写。
func (s *Store) UpdateVideoStatus(id int64, status model.VideoStatus) error {
	if s.db != nil {
		res, err := s.db.ExecContext(context.Background(),
			`UPDATE video SET status = ?, updated_at = ? WHERE video_id = ?`,
			status, nowMilli(), id,
		)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.videos[id]
	if !ok {
		return ErrNotFound
	}
	v.Status = status
	return nil
}

// UpdateVideoPlayback 更新默认播放地址和封面地址。
func (s *Store) UpdateVideoPlayback(id int64, playURL, coverURL string) error {
	return s.UpdateVideoPlaybackURLs(id, playURL, coverURL, nil)
}

// UpdateVideoPlaybackURLs 更新默认播放地址、封面地址和多清晰度播放地址。
func (s *Store) UpdateVideoPlaybackURLs(id int64, playURL, coverURL string, playURLs map[string]string) error {
	if s.db != nil {
		playURLsJSON := ""
		if len(playURLs) > 0 {
			data, err := json.Marshal(playURLs)
			if err != nil {
				return err
			}
			playURLsJSON = string(data)
		}
		res, err := s.db.ExecContext(context.Background(),
			`UPDATE video
			    SET play_url = IF(? = '', play_url, ?),
			        play_urls = IF(? = '', play_urls, ?),
			        cover_url = IF(? = '', cover_url, ?),
			        updated_at = ?
			  WHERE video_id = ?`,
			playURL, playURL, playURLsJSON, playURLsJSON, coverURL, coverURL, nowMilli(), id,
		)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.videos[id]
	if !ok {
		return ErrNotFound
	}
	if playURL != "" {
		v.PlayURL = playURL
	}
	if len(playURLs) > 0 {
		v.PlayURLs = playURLs
	}
	if coverURL != "" {
		v.CoverURL = coverURL
	}
	return nil
}

// ListVideos 广场流:全部视频按发布时间倒序分页。
// 由于 ID 单调递增,按 ID 倒序即为按时间倒序。
// maxID=0 表示从最新开始;否则只返回 ID < maxID 的视频(游标分页)。
func (s *Store) ListVideos(maxID int64, limit int) []model.Video {
	limit = clampLimit(limit)
	if s.db != nil {
		query := `SELECT v.video_id, v.author_id, v.title, v.play_url, COALESCE(v.play_urls, ''), v.cover_url, v.duration, v.width, v.height,
		                 v.file_size, v.status, v.created_at, COALESCE(st.like_count, 0), COALESCE(st.comment_count, 0)
		            FROM video v
		            LEFT JOIN video_stats st ON st.video_id = v.video_id`
		args := []interface{}{}
		if maxID > 0 {
			query += ` WHERE v.video_id < ?`
			args = append(args, maxID)
		}
		query += ` ORDER BY v.video_id DESC LIMIT ?`
		args = append(args, limit)
		rows, err := s.db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return nil
		}
		defer rows.Close()
		return scanVideos(rows)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]model.Video, 0)
	for _, v := range s.videos {
		if maxID > 0 && v.ID >= maxID {
			continue
		}
		res = append(res, *v)
	}
	sortByIDDesc(res)
	return truncate(res, limit)
}

// ListUserVideos 某用户发布的视频(按时间倒序分页)。
func (s *Store) ListUserVideos(authorID, maxID int64, limit int) ([]model.Video, error) {
	limit = clampLimit(limit)
	if s.db != nil {
		if _, err := s.GetUser(authorID); err != nil {
			return nil, err
		}
		query := `SELECT v.video_id, v.author_id, v.title, v.play_url, COALESCE(v.play_urls, ''), v.cover_url, v.duration, v.width, v.height,
		                 v.file_size, v.status, v.created_at, COALESCE(st.like_count, 0), COALESCE(st.comment_count, 0)
		            FROM video v
		            LEFT JOIN video_stats st ON st.video_id = v.video_id
		           WHERE v.author_id = ?`
		args := []interface{}{authorID}
		if maxID > 0 {
			query += ` AND v.video_id < ?`
			args = append(args, maxID)
		}
		query += ` ORDER BY v.video_id DESC LIMIT ?`
		args = append(args, limit)
		rows, err := s.db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanVideos(rows), nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.users[authorID]; !ok {
		return nil, ErrNotFound
	}
	res := make([]model.Video, 0)
	for _, v := range s.videos {
		if v.AuthorID != authorID {
			continue
		}
		if maxID > 0 && v.ID >= maxID {
			continue
		}
		res = append(res, *v)
	}
	sortByIDDesc(res)
	return truncate(res, limit), nil
}

// ---------------- 点赞 ----------------

// Like 点赞(幂等)。changed=true 表示状态确实发生改变。
func (s *Store) Like(userID, videoID int64) (changed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[userID]; !ok {
		return false, ErrNotFound
	}
	v, ok := s.videos[videoID]
	if !ok {
		return false, ErrNotFound
	}
	set := s.userLikes[userID]
	if set == nil {
		set = make(map[int64]struct{})
		s.userLikes[userID] = set
	}
	if _, exists := set[videoID]; exists {
		return false, nil // 已点赞,幂等返回
	}
	set[videoID] = struct{}{}
	v.LikeCount++
	return true, nil
}

// Unlike 取消点赞(幂等)。
func (s *Store) Unlike(userID, videoID int64) (changed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[userID]; !ok {
		return false, ErrNotFound
	}
	v, ok := s.videos[videoID]
	if !ok {
		return false, ErrNotFound
	}
	set := s.userLikes[userID]
	if set == nil {
		return false, nil
	}
	if _, exists := set[videoID]; !exists {
		return false, nil
	}
	delete(set, videoID)
	if v.LikeCount > 0 {
		v.LikeCount--
	}
	return true, nil
}

// HasLiked 当前用户是否点赞过该视频。
func (s *Store) HasLiked(userID, videoID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	set := s.userLikes[userID]
	if set == nil {
		return false
	}
	_, ok := set[videoID]
	return ok
}

// BatchHasLiked 批量查询点赞状态(用于信息流一次性补全)。
func (s *Store) BatchHasLiked(userID int64, videoIDs []int64) map[int64]bool {
	res := make(map[int64]bool, len(videoIDs))
	s.mu.RLock()
	defer s.mu.RUnlock()
	set := s.userLikes[userID]
	for _, vid := range videoIDs {
		if set == nil {
			res[vid] = false
			continue
		}
		_, ok := set[vid]
		res[vid] = ok
	}
	return res
}

// ---------------- 评论 ----------------

// AddComment 发表评论。
func (s *Store) AddComment(videoID, userID int64, content string) (model.Comment, error) {
	if content == "" {
		return model.Comment{}, ErrInvalid
	}
	if s.db != nil {
		if _, err := s.GetUser(userID); err != nil {
			return model.Comment{}, err
		}
		if _, err := s.GetVideo(videoID); err != nil {
			return model.Comment{}, err
		}
		ts := nowMilli()
		tx, err := s.db.BeginTx(context.Background(), nil)
		if err != nil {
			return model.Comment{}, err
		}
		defer tx.Rollback()
		res, err := tx.ExecContext(context.Background(),
			`INSERT INTO comment (video_id, user_id, content, created_at) VALUES (?, ?, ?, ?)`,
			videoID, userID, content, ts,
		)
		if err != nil {
			return model.Comment{}, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return model.Comment{}, err
		}
		if _, err := tx.ExecContext(context.Background(),
			`INSERT INTO video_stats (video_id, like_count, comment_count, updated_at)
			 VALUES (?, 0, 1, ?)
			 ON DUPLICATE KEY UPDATE comment_count = comment_count + 1, updated_at = ?`,
			videoID, ts, ts,
		); err != nil {
			return model.Comment{}, err
		}
		if err := tx.Commit(); err != nil {
			return model.Comment{}, err
		}
		return model.Comment{ID: id, VideoID: videoID, UserID: userID, Content: content, CreatedAt: ts}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[userID]; !ok {
		return model.Comment{}, ErrNotFound
	}
	v, ok := s.videos[videoID]
	if !ok {
		return model.Comment{}, ErrNotFound
	}
	s.commentSeq++
	c := &model.Comment{
		ID:        s.commentSeq,
		VideoID:   videoID,
		UserID:    userID,
		Content:   content,
		CreatedAt: nowMilli(),
	}
	s.comments[c.ID] = c
	s.videoComments[videoID] = append(s.videoComments[videoID], c.ID)
	v.CommentCount++
	return *c, nil
}

// ListComments 列出某视频的全部评论(按发表时间正序)。
func (s *Store) ListComments(videoID int64) ([]model.Comment, error) {
	if s.db != nil {
		if _, err := s.GetVideo(videoID); err != nil {
			return nil, err
		}
		rows, err := s.db.QueryContext(context.Background(),
			`SELECT comment_id, video_id, user_id, content, created_at
			   FROM comment
			  WHERE video_id = ?
			  ORDER BY comment_id ASC`,
			videoID,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		res := make([]model.Comment, 0)
		for rows.Next() {
			var c model.Comment
			if err := rows.Scan(&c.ID, &c.VideoID, &c.UserID, &c.Content, &c.CreatedAt); err != nil {
				return nil, err
			}
			res = append(res, c)
		}
		return res, rows.Err()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.videos[videoID]; !ok {
		return nil, ErrNotFound
	}
	ids := s.videoComments[videoID]
	res := make([]model.Comment, 0, len(ids))
	for _, id := range ids {
		if c, ok := s.comments[id]; ok {
			res = append(res, *c)
		}
	}
	return res, nil
}

// ---------------- 关注 ----------------

// Follow 关注(幂等)。不允许关注自己。
func (s *Store) Follow(followerID, followeeID int64) error {
	if followerID == followeeID {
		return ErrInvalid
	}
	if s.db != nil {
		if _, err := s.GetUser(followerID); err != nil {
			return err
		}
		if _, err := s.GetUser(followeeID); err != nil {
			return err
		}
		ts := nowMilli()
		tx, err := s.db.BeginTx(context.Background(), nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(context.Background(),
			`INSERT IGNORE INTO following (user_id, followee_id, created_at) VALUES (?, ?, ?)`,
			followerID, followeeID, ts,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(context.Background(),
			`INSERT IGNORE INTO follower (followee_id, user_id, created_at) VALUES (?, ?, ?)`,
			followeeID, followerID, ts,
		); err != nil {
			return err
		}
		return tx.Commit()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[followerID]; !ok {
		return ErrNotFound
	}
	if _, ok := s.users[followeeID]; !ok {
		return ErrNotFound
	}
	if s.following[followerID] == nil {
		s.following[followerID] = make(map[int64]struct{})
	}
	if s.followers[followeeID] == nil {
		s.followers[followeeID] = make(map[int64]struct{})
	}
	s.following[followerID][followeeID] = struct{}{}
	s.followers[followeeID][followerID] = struct{}{}
	return nil
}

// Unfollow 取消关注(幂等)。
func (s *Store) Unfollow(followerID, followeeID int64) error {
	if s.db != nil {
		tx, err := s.db.BeginTx(context.Background(), nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(context.Background(),
			`DELETE FROM following WHERE user_id = ? AND followee_id = ?`,
			followerID, followeeID,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(context.Background(),
			`DELETE FROM follower WHERE followee_id = ? AND user_id = ?`,
			followeeID, followerID,
		); err != nil {
			return err
		}
		return tx.Commit()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.following[followerID]; m != nil {
		delete(m, followeeID)
	}
	if m := s.followers[followeeID]; m != nil {
		delete(m, followerID)
	}
	return nil
}

// FollowingFeed 关注流:返回用户所关注的人发布的视频(按时间倒序分页)。
// 这是设计文档里"读扩散/拉模型"的最简实现;数据量大时应改为收件箱写扩散。
func (s *Store) FollowingFeed(userID, maxID int64, limit int) ([]model.Video, error) {
	limit = clampLimit(limit)
	if s.db != nil {
		if _, err := s.GetUser(userID); err != nil {
			return nil, err
		}
		query := `SELECT v.video_id, v.author_id, v.title, v.play_url, COALESCE(v.play_urls, ''), v.cover_url, v.duration, v.width, v.height,
		                 v.file_size, v.status, v.created_at, COALESCE(st.like_count, 0), COALESCE(st.comment_count, 0)
		            FROM video v
		            JOIN following f ON f.followee_id = v.author_id
		            LEFT JOIN video_stats st ON st.video_id = v.video_id
		           WHERE f.user_id = ?`
		args := []interface{}{userID}
		if maxID > 0 {
			query += ` AND v.video_id < ?`
			args = append(args, maxID)
		}
		query += ` ORDER BY v.video_id DESC LIMIT ?`
		args = append(args, limit)
		rows, err := s.db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanVideos(rows), nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.users[userID]; !ok {
		return nil, ErrNotFound
	}
	followees := s.following[userID]
	res := make([]model.Video, 0)
	for _, v := range s.videos {
		if _, ok := followees[v.AuthorID]; !ok {
			continue
		}
		if maxID > 0 && v.ID >= maxID {
			continue
		}
		res = append(res, *v)
	}
	sortByIDDesc(res)
	return truncate(res, limit), nil
}

// ListFollowers 分页拉取某用户的粉丝列表，供写扩散 Worker 使用。
// cursor=0 从头开始；cursor>0 返回 ID 严格大于 cursor 的条目。
// 返回的 next=0 表示已到末页。
func (s *Store) ListFollowers(authorID, cursor int64, limit int) ([]int64, int64, error) {
	if s.db != nil {
		var rows *sql.Rows
		var err error
		if cursor > 0 {
			rows, err = s.db.QueryContext(context.Background(),
				`SELECT user_id FROM follower WHERE followee_id = ? AND user_id > ? ORDER BY user_id ASC LIMIT ?`,
				authorID, cursor, limit+1,
			)
		} else {
			rows, err = s.db.QueryContext(context.Background(),
				`SELECT user_id FROM follower WHERE followee_id = ? ORDER BY user_id ASC LIMIT ?`,
				authorID, limit+1,
			)
		}
		if err != nil {
			return nil, 0, err
		}
		defer rows.Close()
		ids := make([]int64, 0, limit+1)
		for rows.Next() {
			var uid int64
			if err := rows.Scan(&uid); err != nil {
				return nil, 0, err
			}
			ids = append(ids, uid)
		}
		if err := rows.Err(); err != nil {
			return nil, 0, err
		}
		var next int64
		if len(ids) > limit {
			next = ids[limit-1]
			ids = ids[:limit]
		}
		return ids, next, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	set := s.followers[authorID]
	all := make([]int64, 0, len(set))
	for uid := range set {
		all = append(all, uid)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })

	start := 0
	if cursor > 0 {
		for i, uid := range all {
			if uid > cursor {
				start = i
				break
			}
			start = len(all) // cursor >= 所有 ID，返回空
		}
	}
	if start >= len(all) {
		return nil, 0, nil
	}
	end := start + limit
	if end > len(all) {
		end = len(all)
	}
	page := all[start:end]
	var next int64
	if end < len(all) {
		next = page[len(page)-1]
	}
	return page, next, nil
}

// ListFollowees 返回 userID 关注的所有人的 ID。
func (s *Store) ListFollowees(userID int64) ([]int64, error) {
	if s.db != nil {
		rows, err := s.db.QueryContext(context.Background(),
			`SELECT followee_id FROM following WHERE user_id = ?`,
			userID,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := make([]int64, 0)
		for rows.Next() {
			var uid int64
			if err := rows.Scan(&uid); err != nil {
				return nil, err
			}
			out = append(out, uid)
		}
		return out, rows.Err()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	set := s.following[userID]
	out := make([]int64, 0, len(set))
	for uid := range set {
		out = append(out, uid)
	}
	return out, nil
}

// ---------------- 演示数据 ----------------

// Seed 注入演示数据,启动后即可直接体验各接口。
func (s *Store) Seed() {
	alice, _ := s.CreateUser("alice", "password123")
	bob, _ := s.CreateUser("bob", "password123")
	carol, _ := s.CreateUser("carol", "password123")

	// 视频地址为占位 URL;可通过 POST /api/upload 上传真实视频后再发布。
	s.CreateVideo(alice.ID, "猫咪的一天", "/uploads/sample-1.mp4", "", 30, 1920, 1080, 0)
	s.CreateVideo(bob.ID, "海边日落", "/uploads/sample-2.mp4", "", 25, 1920, 1080, 0)
	s.CreateVideo(alice.ID, "做一顿简单的早餐", "/uploads/sample-3.mp4", "", 45, 1920, 1080, 0)
	s.CreateVideo(carol.ID, "城市夜骑", "/uploads/sample-4.mp4", "", 60, 1280, 720, 0)

	// carol 关注 alice 和 bob,便于演示关注流
	_ = s.Follow(carol.ID, alice.ID)
	_ = s.Follow(carol.ID, bob.ID)
}

// ---------------- 内部小工具 ----------------

func clampLimit(limit int) int {
	if limit <= 0 || limit > 50 {
		return 10
	}
	return limit
}

func sortByIDDesc(vs []model.Video) {
	sort.Slice(vs, func(i, j int) bool { return vs[i].ID > vs[j].ID })
}

func truncate(vs []model.Video, limit int) []model.Video {
	if len(vs) > limit {
		return vs[:limit]
	}
	return vs
}

func scanVideos(rows *sql.Rows) []model.Video {
	res := make([]model.Video, 0)
	for rows.Next() {
		var v model.Video
		var status int
		var playURLsJSON string
		if err := rows.Scan(&v.ID, &v.AuthorID, &v.Title, &v.PlayURL, &playURLsJSON, &v.CoverURL, &v.Duration,
			&v.Width, &v.Height, &v.FileSize, &status, &v.CreatedAt, &v.LikeCount, &v.CommentCount); err != nil {
			return res
		}
		v.Status = model.VideoStatus(status)
		v.PlayURLs = decodePlayURLs(playURLsJSON)
		res = append(res, v)
	}
	return res
}

func decodePlayURLs(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
