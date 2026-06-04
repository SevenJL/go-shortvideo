package like

import (
	"context"
	"database/sql"
	"fmt"
)

// MysqlRepo 实现 like.Repo，将点赞记录和计数快照持久化到 MySQL。
type MysqlRepo struct {
	db *sql.DB
}

func NewMysqlRepo(db *sql.DB) *MysqlRepo {
	return &MysqlRepo{db: db}
}

// UpsertLike 幂等写入点赞记录。
// INSERT ... ON DUPLICATE KEY UPDATE + updated_at 比较保证不覆盖更新的状态。
func (r *MysqlRepo) UpsertLike(ctx context.Context, uid, vid, ts int64, liked bool) error {
	status := int8(0)
	if liked {
		status = 1
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO like_record (user_id, video_id, status, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   status = IF(VALUES(updated_at) > updated_at, VALUES(status), status),
		   updated_at = IF(VALUES(updated_at) > updated_at, VALUES(updated_at), updated_at)`,
		uid, vid, status, ts,
	)
	return err
}

// ApplyCountDeltas 批量更新视频统计计数快照。
func (r *MysqlRepo) ApplyCountDeltas(ctx context.Context, deltas map[int64]int64) error {
	if len(deltas) == 0 {
		return nil
	}
	for vid, delta := range deltas {
		_, err := r.db.ExecContext(ctx,
			`INSERT INTO video_stats (video_id, like_count, updated_at)
			 VALUES (?, GREATEST(0, ?), ?)
			 ON DUPLICATE KEY UPDATE
			   like_count = GREATEST(0, like_count + ?),
			   updated_at = ?`,
			vid, delta, delta, delta, delta, // VALUES 不能在 ON DUPLICATE 中引用
		)
		if err != nil {
			return fmt.Errorf("apply count delta video=%d: %w", vid, err)
		}
	}
	return nil
}

// GetLikeCount 从 MySQL 读取视频点赞数快照（用于对账/回源）。
func (r *MysqlRepo) GetLikeCount(ctx context.Context, vid int64) (int64, error) {
	var cnt int64
	err := r.db.QueryRowContext(ctx,
		`SELECT like_count FROM video_stats WHERE video_id = ?`, vid,
	).Scan(&cnt)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return cnt, err
}

// GetLikeRecordsByVideo 查询某视频的所有点赞用户（用于对账重建）。
func (r *MysqlRepo) GetLikeRecordsByVideo(ctx context.Context, vid int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT user_id FROM like_record WHERE video_id = ? AND status = 1`, vid,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		ids = append(ids, uid)
	}
	return ids, rows.Err()
}
