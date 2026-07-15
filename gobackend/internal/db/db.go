// Package db 初始化 GORM 连接 + AutoMigrate。
//
// 优先 MysqlDSN (DB_DSN 形如 "user:pass@tcp(host:3306)/db?...")，
// 否则 SQLite (DB_DSN 文件路径)。同时把 DB_DRIVER env 显式控制。
package db

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Config 简化构造参数。
type Config struct {
	Driver   string // "sqlite3" (默认) or "mysql"
	DSN      string // sqlite 文件路径 or mysql DSN
	LogLevel logger.LogLevel
}

// FromEnv 从环境变量构造配置。
func FromEnv() Config {
	c := Config{
		Driver:   strings.ToLower(os.Getenv("DB_DRIVER")),
		DSN:      os.Getenv("DB_DSN"),
		LogLevel: logger.Warn,
	}
	if c.Driver == "" {
		c.Driver = "sqlite3"
	}
	if c.DSN == "" {
		c.DSN = "/app/data/db.sqlite3"
	}
	return c
}

// Open 打开 GORM 连接，驱动由 Driver 决定。
func Open(c Config) (*gorm.DB, error) {
	gormCfg := &gorm.Config{
		// 关闭自动外键约束 / 自动复数表名 — 与 Django 已建表兼容。
		DisableForeignKeyConstraintWhenMigrating: true,
		Logger: logger.Default.LogMode(c.LogLevel),
		NowFunc: func() time.Time { return time.Now() },
	}

	var (
		db  *gorm.DB
		err error
	)
	switch c.Driver {
	case "mysql":
		db, err = gorm.Open(mysql.Open(c.DSN), gormCfg)
	case "sqlite3", "sqlite":
		db, err = gorm.Open(sqlite.Open(c.DSN), gormCfg)
	default:
		return nil, fmt.Errorf("unsupported db driver: %q", c.Driver)
	}
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	sqlDB, sErr := db.DB()
	if sErr == nil {
		sqlDB.SetMaxIdleConns(5)
		sqlDB.SetMaxOpenConns(20)
		sqlDB.SetConnMaxLifetime(time.Hour)

		// P1.5 issue F (M7): SQLite durability + concurrency PRAGMAs.
		//
		//   journal_mode=WAL    — readers don't block writers; essential
		//                         when scanner (worker) and gateway both
		//                         hold the SQLite file open concurrently.
		//                         Returns the new mode in a row; we ignore
		//                         the row and only log on error.
		//   synchronous=NORMAL  — WAL default; trades a tiny crash-window
		//                         risk for big fsync reduction. SAFE with
		//                         WAL.
		//   busy_timeout=5000   — wait up to 5 s for a write lock before
		//                         returning SQLITE_BUSY. Without it, an
		//                         unlucky scanner-vs-gateway race returns
		//                         errors that propagate as 500s.
		//   cache_size=-2000    — ~2 MiB negative-cache (KiB). Modest
		//                         budget for a music metadata DB; tune
		//                         per deployment if memory headroom grows.
		if c.Driver == "sqlite3" || c.Driver == "sqlite" {
			pragmas := []struct{ name, sql string }{
				{"journal_mode=WAL", "PRAGMA journal_mode=WAL"},
				{"synchronous=NORMAL", "PRAGMA synchronous=NORMAL"},
				{"busy_timeout=5000", "PRAGMA busy_timeout=5000"},
				{"cache_size=-2000", "PRAGMA cache_size=-2000"},
			}
			for _, p := range pragmas {
				if _, perr := sqlDB.Exec(p.sql); perr != nil {
					log.Printf("[db] %s failed: %v", p.name, perr)
				}
			}
		}
	}
	return db, nil
}

// AutoMigrate 创建/更新所有表。
//
// 注册表覆盖：
//
//   音乐核心: Folder / Task / TaskRecord / Track / Album / Artist /
//             Genre / Attachment
//   认证 / 音乐应用镜像: User / UserProfile / Playlist / TrackFavorite /
//                       PlaylistTrack
//
// 表顺序按父→子便利排列（User → UserProfile / Playlist → TrackFavorite）。
// PlaylistTrack 是 Playlist 与 Track 之间的多对多连接表——Track 已在上文
// 已迁，PlaylistTrack 附于末尾。GORM 在 DisableForeignKeyConstraintWhenMigrating
// 下不强制外键；顺序仅出于规范化，实际不能出错。
//
// 历史: 之前注册表只覆盖音乐核心; tests 要重复调用 gormDB.AutoMigrate(
// &db.User{}, &db.UserProfile{}, &db.Playlist{}, &db.TrackFavorite{})。
// PlaylistTrack 以服务 getPlaylists 的 songCount/duration 计算。
func AutoMigrate(db *gorm.DB) error {
	log.Printf("[db] running auto-migrate")
	return db.AutoMigrate(
		&Folder{},
		&Task{},
		&TaskRecord{},
		&Track{},
		&Album{},
		&Artist{},
		&Genre{},
		&Attachment{},
		// Auth + music-app mirror models (Was: "Subsonic-aligned" before
		// Subsonic removal — kept here so Django↔Go DB parity survives):
		&User{},
		&UserProfile{},
		&Playlist{},
		&TrackFavorite{},
		// P1.5 issue D: multi-pair 列表中间表
		&PlaylistTrack{},
	)
}
