// Package database 提供 PostgreSQL 连接与内嵌 SQL 迁移执行器。
package database

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Connect 建立 GORM 连接（带重试），数据库不可达时按 interval 重试 retry 次。
func Connect(dsn string, retry, intervalMs int) (*gorm.DB, error) {
	if retry <= 0 {
		retry = 1
	}
	var db *gorm.DB
	var lastErr error
	for i := 0; i < retry; i++ {
		db, lastErr = gorm.Open(postgres.Open(dsn), &gorm.Config{
			Logger: gormlogger.Default.LogMode(gormlogger.Warn),
		})
		if lastErr == nil {
			if lastErr = connectPing(db); lastErr == nil {
				return db, nil
			}
		}
		if i < retry-1 {
			time.Sleep(time.Duration(intervalMs) * time.Millisecond)
		}
	}
	return nil, fmt.Errorf("数据库连接失败: %w", lastErr)
}

func connectPing(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return sqlDB.PingContext(ctx)
}

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate 按版本号顺序执行内嵌的 *.up.sql 迁移（幂等：已应用的版本跳过）。
func Migrate(db *gorm.DB) error {
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`).Error; err != nil {
		return fmt.Errorf("创建迁移记录表失败: %w", err)
	}

	entries, err := fs.Glob(migrationsFS, "migrations/*.up.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)

	for _, entry := range entries {
		var version int64
		if _, err := fmt.Sscanf(strings.TrimSuffix(fsName(entry), ".up.sql"), "%d", &version); err != nil {
			return fmt.Errorf("迁移文件名非法（须为 NNNN_*.up.sql）: %s", entry)
		}
		var done int64
		if err := db.Raw(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&done).Error; err != nil {
			return err
		}
		if done > 0 {
			continue
		}
		body, err := migrationsFS.ReadFile(entry)
		if err != nil {
			return err
		}
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(string(body)).Error; err != nil {
				return err
			}
			return tx.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, version).Error
		}); err != nil {
			return fmt.Errorf("执行迁移 %s 失败: %w", entry, err)
		}
	}
	return nil
}

func fsName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}
