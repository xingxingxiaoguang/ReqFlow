// Package repository 以 GORM + pgvector 实现 port 仓储契约。
// 本包只依赖 port/domain 与注入的 *gorm.DB，不感知业务用例与 HTTP。
package repository

import (
	"time"

	"github.com/pgvector/pgvector-go"
)

/* ---- 表行结构（与 migrations 对齐；向量列用 pgvector 类型） ---- */

type projectRow struct {
	ID              string           `gorm:"column:id;primaryKey"`
	Name            string           `gorm:"column:name"`
	Description     string           `gorm:"column:description"`
	RemoteUpdatedAt *string          `gorm:"column:remote_updated_at"`
	IsArchived      bool             `gorm:"column:is_archived"`
	Embedding       *pgvector.Vector `gorm:"column:embedding"`
	SyncedAt        time.Time        `gorm:"column:synced_at"`
}

func (projectRow) TableName() string { return "projects" }

type workItemRow struct {
	ID              string           `gorm:"column:id;primaryKey"`
	ProjectID       string           `gorm:"column:project_id"`
	Identifier      string           `gorm:"column:identifier"`
	Title           string           `gorm:"column:title"`
	Description     string           `gorm:"column:description"`
	Kind            string           `gorm:"column:kind"`
	TypeID          string           `gorm:"column:type_id"`
	StateID         string           `gorm:"column:state_id"`
	RemoteUpdatedAt *string          `gorm:"column:remote_updated_at"`
	IsArchived      bool             `gorm:"column:is_archived"`
	Embedding       *pgvector.Vector `gorm:"column:embedding"`
	SyncedAt        time.Time        `gorm:"column:synced_at"`
}

func (workItemRow) TableName() string { return "work_items" }

type metaTypeRow struct {
	ID        string `gorm:"column:id;primaryKey"`
	ProjectID string `gorm:"column:project_id;primaryKey"`
	Name      string `gorm:"column:name"`
	Group     string `gorm:"column:group"`
}

func (metaTypeRow) TableName() string { return "work_item_types" }

type metaStateRow struct {
	ID             string `gorm:"column:id;primaryKey"`
	ProjectID      string `gorm:"column:project_id;primaryKey"`
	WorkItemTypeID string `gorm:"column:work_item_type_id;primaryKey"`
	Name           string `gorm:"column:name"`
	Type           string `gorm:"column:type"`
	Color          string `gorm:"column:color"`
}

func (metaStateRow) TableName() string { return "work_item_states" }

type metaPriorityRow struct {
	ID        string `gorm:"column:id;primaryKey"`
	ProjectID string `gorm:"column:project_id;primaryKey"`
	Name      string `gorm:"column:name"`
}

func (metaPriorityRow) TableName() string { return "work_item_priorities" }

type importRecordRow struct {
	ID                string     `gorm:"column:id;primaryKey"`
	FileName          string     `gorm:"column:file_name"`
	OriginalFilePath  *string    `gorm:"column:original_file_path"`
	Status            string     `gorm:"column:status"`
	ItemsCount        int        `gorm:"column:items_count"`
	TargetProjectID   *string    `gorm:"column:target_project_id"`
	TargetProjectName *string    `gorm:"column:target_project_name"`
	ImportedCount     int        `gorm:"column:imported_count"`
	FailedCount       int        `gorm:"column:failed_count"`
	ErrorMessage      *string    `gorm:"column:error_message"`
	AgentContext      *string    `gorm:"column:agent_context"` // 会话 JSON 文本（refine 载体）
	CreatedAt         time.Time  `gorm:"column:created_at"`
	UpdatedAt         time.Time  `gorm:"column:updated_at"`
}

func (importRecordRow) TableName() string { return "import_records" }

type importRecordItemRow struct {
	ID                  string    `gorm:"column:id;primaryKey"`
	RecordID            string    `gorm:"column:record_id;index"`
	Title               string    `gorm:"column:title"`
	Description         string    `gorm:"column:description"`
	ProjectName         string    `gorm:"column:project_name"`
	TypeID              string    `gorm:"column:type_id"`
	Priority            string    `gorm:"column:priority"`
	EstimatedHours      *float64  `gorm:"column:estimated_hours"`
	StartAt             *string   `gorm:"column:start_at"`
	EndAt               *string   `gorm:"column:end_at"`
	AssigneeName        *string   `gorm:"column:assignee_name"`
	SolutionSuggestion  string    `gorm:"column:solution_suggestion"`
	PingCodeID          *string   `gorm:"column:pingcode_id"`
	PingCodeIdentifier  *string   `gorm:"column:pingcode_identifier"`
	Status              string    `gorm:"column:status"`
	ErrorMessage        *string   `gorm:"column:error_message"`
	CreatedAt           time.Time `gorm:"column:created_at"`
}

func (importRecordItemRow) TableName() string { return "import_record_items" }

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
