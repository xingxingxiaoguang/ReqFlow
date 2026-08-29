//go:build integration

package database

import (
	"fmt"
	"testing"
	"time"
)

// TestIntegrationFreshMigration 在独立临时数据库执行完整迁移链，避免开发库已经
// 记录某个版本时掩盖同版本 SQL 的语法或约束变更。
func TestIntegrationFreshMigration(t *testing.T) {
	admin, err := Connect("postgres://reqflow:reqflow@127.0.0.1:5432/postgres?sslmode=disable", 1, 0)
	if err != nil {
		t.Skipf("本地 PG 不可用，跳过全新迁移测试: %v", err)
	}
	databaseName := fmt.Sprintf("reqflow_migration_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE DATABASE " + databaseName).Error; err != nil {
		t.Fatalf("创建临时数据库: %v", err)
	}
	t.Cleanup(func() {
		_ = admin.Exec("DROP DATABASE IF EXISTS " + databaseName + " WITH (FORCE)").Error
	})

	target, err := Connect("postgres://reqflow:reqflow@127.0.0.1:5432/"+databaseName+"?sslmode=disable", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(target); err != nil {
		t.Fatal(err)
	}
	var version int64
	if err := target.Raw(`SELECT max(version) FROM schema_migrations`).Scan(&version).Error; err != nil {
		t.Fatal(err)
	}
	if version != 14 {
		t.Fatalf("latest migration=%d want=14", version)
	}
	var tables int64
	if err := target.Raw(`SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name IN ('record_draft_sets','extraction_units','record_drafts')`).
		Scan(&tables).Error; err != nil {
		t.Fatal(err)
	}
	if tables != 3 {
		t.Fatalf("extraction tables=%d want=3", tables)
	}
	if sqlDB, err := target.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
