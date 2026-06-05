package store

import (
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

	users    map[int64]*model.User
	userByName map[string]*model.User // username → user, O(1) lookup
	videos   map[int64]*model.Video
	comments map[int64]*model.Comment

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
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.videos[id]
	if !ok {
		return model.Video{}, ErrNotFound
	}
	return *v, nil
}

// ListVideos 广场流:全部视频按发布时间倒序分页。
// 由于 ID 单调递增,按 ID 倒序即为按时间倒序。
// maxID=0 表示从最新开始;否则只返回 ID < maxID 的视频(游标分页)。
func (s *Store) ListVideos(maxID int64, limit int) []model.Video {
	limit = clampLimit(limit)
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
