// Package repository 以 GORM + pgvector 实现 port 仓储契约。
// 本包只依赖 port/domain 与注入的 *gorm.DB，不感知业务用例与 HTTP。
package repository

import (
	"time"

	"github.com/pgvector/pgvector-go"
)

/* ---- 表行结构（与 migrations 对齐；向量列用 pgvector 类型） ---- */

type datasetRow struct {
	ID            string    `gorm:"column:id;primaryKey"`
	Type          string    `gorm:"column:type"`
	Name          string    `gorm:"column:name"`
	Description   string    `gorm:"column:description"`
	Tags          string    `gorm:"column:tags"` // JSON 数组文本
	SourceTaskID  *string   `gorm:"column:source_task_id"`
	Status        string    `gorm:"column:status"`
	ItemCount     int       `gorm:"column:item_count"`
	SchemaVersion int       `gorm:"column:schema_version"`
	Extra         string    `gorm:"column:extra"` // JSON 文本
	CreatedAt     time.Time `gorm:"column:created_at"`
	UpdatedAt     time.Time `gorm:"column:updated_at"`
}

func (datasetRow) TableName() string { return "datasets" }

type datasetItemRow struct {
	ID           string           `gorm:"column:id;primaryKey"`
	DatasetID    string           `gorm:"column:dataset_id;index"`
	Fields       string           `gorm:"column:fields"`
	ItemKey      string           `gorm:"column:item_key"`
	Fingerprint  string           `gorm:"column:fingerprint"`
	Metadata     string           `gorm:"column:metadata"` // JSON 文本
	SourceTaskID *string          `gorm:"column:source_task_id"`
	Embedding    *pgvector.Vector `gorm:"column:embedding"`
	CreatedAt    time.Time        `gorm:"column:created_at"`
	UpdatedAt    time.Time        `gorm:"column:updated_at"`
}

func (datasetItemRow) TableName() string { return "dataset_items" }

type taskRow struct {
	ID                string     `gorm:"column:id;primaryKey"`
	Type              string     `gorm:"column:type"`
	Title             string     `gorm:"column:title"`
	Status            string     `gorm:"column:status"`
	CurrentStep       int        `gorm:"column:current_step"`
	Workflow          *string    `gorm:"column:workflow"`      // 工作流定义快照（JSON 文本）
	Input             *string    `gorm:"column:input"`        // JSON 文本
	Output            *string    `gorm:"column:output"`       // JSON 文本
	AgentContext      *string    `gorm:"column:agent_context"` // 会话 JSON 文本（续跑载体）
	ItemsCount        int        `gorm:"column:items_count"`
	ImportedCount     int        `gorm:"column:imported_count"`
	FailedCount       int        `gorm:"column:failed_count"`
	TargetProjectID   *string    `gorm:"column:target_project_id"`
	TargetProjectName *string    `gorm:"column:target_project_name"`
	OutputDatasetID   *string    `gorm:"column:output_dataset_id"`
	InputDatasetID    *string    `gorm:"column:input_dataset_id"`
	ErrorMessage      *string    `gorm:"column:error_message"`
	CreatedAt         time.Time  `gorm:"column:created_at"`
	UpdatedAt         time.Time  `gorm:"column:updated_at"`
	StartedAt         *time.Time `gorm:"column:started_at"`
	FinishedAt        *time.Time `gorm:"column:finished_at"`
}

func (taskRow) TableName() string { return "tasks" }

type taskStepRow struct {
	ID        string     `gorm:"column:id;primaryKey"`
	TaskID    string     `gorm:"column:task_id;index"`
	Seq       int        `gorm:"column:seq"`
	Name      string     `gorm:"column:name"`
	Status    string     `gorm:"column:status"`
	Detail    string     `gorm:"column:detail"`
	Data      *string    `gorm:"column:data"` // JSON 文本：工具轨迹/导入汇总
	StartedAt *time.Time `gorm:"column:started_at"`
	EndedAt   *time.Time `gorm:"column:ended_at"`
}

func (taskStepRow) TableName() string { return "task_steps" }

type taskItemRow struct {
	ID                  string    `gorm:"column:id;primaryKey"`
	TaskID              string    `gorm:"column:task_id;index"`
	Title               string    `gorm:"column:title"`
	Description         string    `gorm:"column:description"`
	ProjectName         string    `gorm:"column:project_name"`
	TypeID              string    `gorm:"column:type_id"`
	Priority            string    `gorm:"column:priority"`
	EstimatedHours      *float64  `gorm:"column:estimated_hours"`
	StartAt             *string   `gorm:"column:start_at"`
	EndAt               *string   `gorm:"column:end_at"`
	AssigneeName        *string   `gorm:"column:assignee_name"`
	State               string    `gorm:"column:state"`
	SolutionSuggestion  string    `gorm:"column:solution_suggestion"`
	Status              string    `gorm:"column:status"`
	ErrorMessage        *string   `gorm:"column:error_message"`
	CreatedAt           time.Time `gorm:"column:created_at"`
}

func (taskItemRow) TableName() string { return "task_items" }

/* ---- 公共构造 ---- */

// NewProjectRepo / NewWorkItemRepo / NewMetaRepo / NewImportRepo 见各自文件。
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
