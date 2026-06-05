package mysqlx

import "database/sql"

// RunMigrations 执行建表 DDL（幂等，使用 IF NOT EXISTS）。
// 生产环境建议用 golang-migrate 等专业工具管理 schema 版本。
func RunMigrations(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS like_record (
			user_id    BIGINT NOT NULL,
			video_id   BIGINT NOT NULL,
			status     TINYINT NOT NULL DEFAULT 1 COMMENT '1=已点赞 0=已取消',
			updated_at BIGINT NOT NULL COMMENT '毫秒时间戳',
			PRIMARY KEY (user_id, video_id),
			INDEX idx_user (user_id),
			INDEX idx_video (video_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS video_stats (
			video_id   BIGINT PRIMARY KEY,
			like_count BIGINT NOT NULL DEFAULT 0,
			comment_count BIGINT NOT NULL DEFAULT 0,
			updated_at BIGINT NOT NULL COMMENT '毫秒时间戳'
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS following (
			user_id     BIGINT NOT NULL COMMENT '粉丝',
			followee_id BIGINT NOT NULL COMMENT '被关注者',
			created_at  BIGINT NOT NULL COMMENT '毫秒时间戳',
			PRIMARY KEY (user_id, followee_id),
			INDEX idx_user (user_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS follower (
			followee_id BIGINT NOT NULL COMMENT '被关注者',
			user_id     BIGINT NOT NULL COMMENT '粉丝',
			created_at  BIGINT NOT NULL COMMENT '毫秒时间戳',
			PRIMARY KEY (followee_id, user_id),
			INDEX idx_followee (followee_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS video (
			video_id    BIGINT PRIMARY KEY,
			author_id   BIGINT NOT NULL,
			title       VARCHAR(255) NOT NULL DEFAULT '',
			play_url    VARCHAR(512) NOT NULL DEFAULT '' COMMENT '原始/默认播放地址',
			cover_url   VARCHAR(512) NOT NULL DEFAULT '',
			duration    INT NOT NULL DEFAULT 0 COMMENT '视频时长(秒)',
			width       INT NOT NULL DEFAULT 0,
			height      INT NOT NULL DEFAULT 0,
			file_size   BIGINT NOT NULL DEFAULT 0,
			status      TINYINT NOT NULL DEFAULT 1 COMMENT '0=上传中 1=转码中 2=已完成 3=失败',
			created_at  BIGINT NOT NULL COMMENT '毫秒时间戳',
			updated_at  BIGINT NOT NULL DEFAULT 0 COMMENT '毫秒时间戳',
			INDEX idx_author (author_id, created_at),
			INDEX idx_status (status, created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
