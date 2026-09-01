// Package repository 以 GORM + 原生 SQL 实现 port 仓储契约。
// 本包只依赖 port/domain 与注入的 *gorm.DB，不感知业务用例与 HTTP。
package repository

import (
	"time"

	"reqflow/internal/domain/model"
)

/* ---- 共享表行结构与行 <-> 模型转换（V2） ---- */

// taskRow 任务表行（V2 列集：定义快照 + 批次派生来源 + 任务级生命周期；
// 步骤级执行状态在 step_runs，不在本表）。
type taskRow struct {
	ID                 string     `gorm:"column:id;primaryKey"`
	WorkspaceID        string     `gorm:"column:workspace_id"`
	DefinitionID       *string    `gorm:"column:definition_id"`
	DefinitionSnapshot *string    `gorm:"column:definition_snapshot;type:jsonb"`
	Type               string     `gorm:"column:type"`
	Title              string     `gorm:"column:title"`
	BatchID            *string    `gorm:"column:batch_id"`
	BatchOrdinal       int        `gorm:"column:batch_ordinal"`
	BatchSize          int        `gorm:"column:batch_size"`
	SourceAssetID      *string    `gorm:"column:source_asset_id"`
	SourceFilename     string     `gorm:"column:source_filename"`
	Status             string     `gorm:"column:status"`
	ErrorMessage       *string    `gorm:"column:error_message"`
	CreatedAt          time.Time  `gorm:"column:created_at"`
	UpdatedAt          time.Time  `gorm:"column:updated_at"`
	StartedAt          *time.Time `gorm:"column:started_at"`
	FinishedAt         *time.Time `gorm:"column:finished_at"`
	ArchivedAt         *time.Time `gorm:"column:archived_at"`
}

func (taskRow) TableName() string { return "tasks" }

func taskToModel(row *taskRow) model.Task {
	return model.Task{
		ID: row.ID, WorkspaceID: row.WorkspaceID, DefinitionID: strVal(row.DefinitionID),
		DefinitionSnapshot: strVal(row.DefinitionSnapshot),
		Type:               row.Type, Title: row.Title, BatchID: strVal(row.BatchID),
		BatchOrdinal: row.BatchOrdinal, BatchSize: row.BatchSize,
		SourceAssetID: strVal(row.SourceAssetID), SourceFilename: row.SourceFilename,
		Status:       row.Status, ErrorMessage: strVal(row.ErrorMessage),
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		StartedAt: timeVal(row.StartedAt), FinishedAt: timeVal(row.FinishedAt),
	}
}

/* ---- 公共构造 ---- */

// 全部依赖经构造函数注入（*gorm.DB），无全局状态。

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func strVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func timeVal(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}
