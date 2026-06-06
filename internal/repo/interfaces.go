// Package repo defines explicit persistence boundaries for production stores.
package repo

import (
	"context"

	"shortvideo/internal/model"
)

type UserRepo interface {
	CreateUser(username, password string) (*model.User, error)
	GetUser(id int64) (*model.User, error)
	GetUserByUsername(username string) (*model.User, error)
	AuthenticateUser(username, password string) (*model.User, error)
	FollowStats(userID int64) (followingCount int, followerCount int)
}

type VideoRepo interface {
	CreateVideo(authorID int64, title, playURL, coverURL string, duration int, width, height int, fileSize int64) (model.Video, error)
	GetVideo(id int64) (model.Video, error)
	ListVideos(maxID int64, limit int) []model.Video
	ListUserVideos(authorID, maxID int64, limit int) ([]model.Video, error)
	UpdateVideoStatus(id int64, status model.VideoStatus) error
	UpdateVideoPlayback(id int64, playURL, coverURL string) error
	UpdateVideoPlaybackURLs(id int64, playURL, coverURL string, playURLs map[string]string) error
}

type CommentRepo interface {
	AddComment(videoID, userID int64, content string) (model.Comment, error)
	ListComments(videoID int64) ([]model.Comment, error)
}

type RelationRepo interface {
	Follow(followerID, followeeID int64) error
	Unfollow(followerID, followeeID int64) error
	FollowingFeed(userID, maxID int64, limit int) ([]model.Video, error)
	ListFollowers(authorID, cursor int64, limit int) ([]int64, int64, error)
	ListFollowees(userID int64) ([]int64, error)
}

type LikeRepo interface {
	Like(userID, videoID int64) (changed bool, err error)
	Unlike(userID, videoID int64) (changed bool, err error)
	HasLiked(userID, videoID int64) bool
	BatchHasLiked(userID int64, videoIDs []int64) map[int64]bool
}

type AsyncLikeRepo interface {
	UpsertLike(ctx context.Context, uid, vid, ts int64, liked bool) error
	ApplyCountDeltas(ctx context.Context, deltas map[int64]int64) error
}
