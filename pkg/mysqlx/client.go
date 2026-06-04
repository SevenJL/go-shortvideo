// Package mysqlx 封装 database/sql 的 MySQL 连接管理。
package mysqlx

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// Config 是 MySQL 连接配置。
type Config struct {
	DSN          string // e.g. "user:password@tcp(127.0.0.1:3306)/dbname?parseTime=true"
	MaxOpenConns int    // 最大连接数，默认 25
	MaxIdleConns int    // 最大空闲连接数，默认 5
	MaxLifetime  time.Duration // 连接最大存活时间，默认 5min
}

func (c *Config) defaults() {
	if c.MaxOpenConns <= 0 {
		c.MaxOpenConns = 25
	}
	if c.MaxIdleConns <= 0 {
		c.MaxIdleConns = 5
	}
	if c.MaxLifetime <= 0 {
		c.MaxLifetime = 5 * time.Minute
	}
}

// NewDB 创建并配置数据库连接池。
func NewDB(cfg Config) (*sql.DB, error) {
	cfg.defaults()
	db, err := sql.Open("mysql", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("mysql open: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.MaxLifetime)
	db.SetConnMaxIdleTime(2 * time.Minute)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("mysql ping: %w", err)
	}
	return db, nil
}
