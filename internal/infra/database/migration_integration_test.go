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
	if version != 5 {
		t.Fatalf("latest migration=%d want=5", version)
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
	var cleaningTables int64
	if err := target.Raw(`SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name IN
		('transformed_record_sets','transformed_records','validation_result_sets','validation_results')`).
		Scan(&cleaningTables).Error; err != nil {
		t.Fatal(err)
	}
	if cleaningTables != 4 {
		t.Fatalf("cleaning tables=%d want=4", cleaningTables)
	}
	var reviewTables int64
	if err := target.Raw(`SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name IN
		('approved_record_sets','record_review_decisions')`).Scan(&reviewTables).Error; err != nil {
		t.Fatal(err)
	}
	if reviewTables != 2 {
		t.Fatalf("review tables=%d want=2", reviewTables)
	}
	var platformConfigTables int64
	if err := target.Raw(`SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name = 'platform_configs'`).Scan(&platformConfigTables).Error; err != nil {
		t.Fatal(err)
	}
	if platformConfigTables != 1 {
		t.Fatalf("platform config tables=%d want=1", platformConfigTables)
	}
	var workflowTables int64
	if err := target.Raw(`SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name IN ('workflows', 'workflow_command_events')`).Scan(&workflowTables).Error; err != nil {
		t.Fatal(err)
	}
	if workflowTables != 2 {
		t.Fatalf("workflow tables=%d want=2", workflowTables)
	}
	var designSessionTables int64
	if err := target.Raw(`SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name = 'workflow_design_sessions'`).Scan(&designSessionTables).Error; err != nil {
		t.Fatal(err)
	}
	if designSessionTables != 1 {
		t.Fatalf("design session tables=%d want=1", designSessionTables)
	}
	var workflowRuntimeTables int64
	if err := target.Raw(`SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name IN
		('workflow_runs', 'workflow_node_runs', 'node_resource_bindings')`).Scan(&workflowRuntimeTables).Error; err != nil {
		t.Fatal(err)
	}
	if workflowRuntimeTables != 3 {
		t.Fatalf("workflow runtime tables=%d want=3", workflowRuntimeTables)
	}
	var legacyTables int64
	if err := target.Raw(`SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name IN
		('projects', 'work_items', 'import_records', 'task_steps', 'task_items',
		 'task_definitions', 'tasks', 'step_runs', 'task_resource_bindings', 'step_resource_bindings',
		 'extraction_profiles', 'retrieval_profiles', 'analysis_profiles',
		 'agent_sessions', 'agent_skills', 'agent_tool_settings',
		 'metadata_registry', 'metadata_audit',
		 'archived_tasks', 'archived_datasets', 'archived_dataset_items')`).Scan(&legacyTables).Error; err != nil {
		t.Fatal(err)
	}
	if legacyTables != 0 {
		t.Fatalf("legacy tables=%d want=0（压平后不得存在旧表）", legacyTables)
	}
	if sqlDB, err := target.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
