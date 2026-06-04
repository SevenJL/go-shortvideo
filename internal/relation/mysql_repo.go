package relation

import (
	"context"
	"database/sql"
)

// MysqlRepo 实现 relation.Repo，将关注关系持久化到 MySQL。
type MysqlRepo struct {
	db *sql.DB
}

func NewMysqlRepo(db *sql.DB) *MysqlRepo {
	return &MysqlRepo{db: db}
}

// FollowerCount 查询作者的粉丝总数。
func (r *MysqlRepo) FollowerCount(ctx context.Context, userID int64) (int64, error) {
	var cnt int64
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM follower WHERE followee_id = ?`, userID,
	).Scan(&cnt)
	return cnt, err
}

// ListFollowers 分页拉取粉丝列表。cursor 为上一页最后一个粉丝 ID，0 从头开始。
// 返回的 next 为当前页最后一条的 user_id，0 表示已到末页。
func (r *MysqlRepo) ListFollowers(ctx context.Context, authorID, cursor int64, limit int) (ids []int64, next int64, err error) {
	var rows *sql.Rows
	if cursor > 0 {
		rows, err = r.db.QueryContext(ctx,
			`SELECT user_id FROM follower
			 WHERE followee_id = ? AND user_id > ?
			 ORDER BY user_id ASC LIMIT ?`,
			authorID, cursor, limit+1, // 多取一条判断是否有下一页
		)
	} else {
		rows, err = r.db.QueryContext(ctx,
			`SELECT user_id FROM follower
			 WHERE followee_id = ?
			 ORDER BY user_id ASC LIMIT ?`,
			authorID, limit+1,
		)
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

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

	// 如果多取了一条，说明有下一页
	if len(ids) > limit {
		next = ids[limit-1]
		ids = ids[:limit]
	}
	return ids, next, nil
}

// ListFollowees 返回用户关注的所有人的 ID 列表。
func (r *MysqlRepo) ListFollowees(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT followee_id FROM following WHERE user_id = ?`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var fid int64
		if err := rows.Scan(&fid); err != nil {
			return nil, err
		}
		ids = append(ids, fid)
	}
	return ids, rows.Err()
}

// Follow 插入关注关系（幂等）。
func (r *MysqlRepo) Follow(ctx context.Context, followerID, followeeID, ts int64) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT IGNORE INTO following (user_id, followee_id, created_at) VALUES (?, ?, ?)`,
		followerID, followeeID, ts,
	)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT IGNORE INTO follower (followee_id, user_id, created_at) VALUES (?, ?, ?)`,
		followeeID, followerID, ts,
	)
	return err
}

// Unfollow 删除关注关系。
func (r *MysqlRepo) Unfollow(ctx context.Context, followerID, followeeID int64) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM following WHERE user_id = ? AND followee_id = ?`,
		followerID, followeeID,
	)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx,
		`DELETE FROM follower WHERE followee_id = ? AND user_id = ?`,
		followeeID, followerID,
	)
	return err
}
