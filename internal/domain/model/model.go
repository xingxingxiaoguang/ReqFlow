// Package model 定义 ReqFlow 的领域实体与值。
// 本包仅依赖标准库，承载同步缓存、分析草稿、导入记录三块核心状态。
package model

import "time"

/* ---- 同步缓存（来自协作平台） ---- */

// Project 已同步的平台项目。
type Project struct {
	ID              string
	Name            string
	Description     string
	RemoteUpdatedAt string // 平台侧最后更新时间原样字符串，增量比对用
	IsArchived      bool
}

// WorkItem 已同步的平台工作项（需求语料库，也是 bug 匹配的底料）。
type WorkItem struct {
	ID              string
	ProjectID       string
	Identifier      string // 平台编号，如 WI-123
	Title           string
	Description     string
	Kind            string // 类型 group：story/task/bug…
	TypeID          string
	StateID         string
	RemoteUpdatedAt string
	IsArchived      bool
}

/* ---- 平台元数据（名称 → UUID 映射） ---- */

type MetaType struct {
	ID        string
	ProjectID string
	Name      string
	Group     string
}

type MetaState struct {
	ID            string
	ProjectID     string
	WorkItemTypeID string
	Name          string
	Type          string // pending | doing | done
	Color         string
}

type MetaPriority struct {
	ID        string
	ProjectID string
	Name      string
}

/* ---- 分析草稿与导入记录 ---- */

// DraftItem LLM 分析产出的工作项草稿（导入前可被用户编辑）。
type DraftItem struct {
	ID                 string `json:"id"`
	ProjectName        string `json:"project_name"`
	Title              string `json:"title"`
	Description        string `json:"description"`
	Priority           string `json:"priority"`           // High | Medium | Low
	EstimatedHours     float64 `json:"estimated_hours"`
	StartAt            string `json:"start_at"`           // ISO 8601
	EndAt              string `json:"end_at,omitempty"`
	TypeID             string `json:"type_id"`            // story | task | bug | feature | epic
	AssigneeName       string `json:"assignee_name"`
	State              string `json:"state"`              // 文档标注的状态名，可空
	SolutionSuggestion string `json:"solution_suggestion"`
}

// ImportRecord 一轮「文档 → 分析 → 导入」的记录。
type ImportRecord struct {
	ID                string
	FileName          string
	OriginalFilePath  string
	Status            string
	ItemsCount        int
	TargetProjectID   string
	TargetProjectName string
	ImportedCount     int
	FailedCount       int
	ErrorMessage      string
	// AgentContext 分析会话的 JSON 序列化（port.Context：系统提示 + 消息序列 + 工具表）。
	// 单发与 agent 模式均落库，是 refine 微调与换模型续跑的统一载体；空 = 未记录。
	AgentContext string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ImportRecordItem 导入明细（与 DraftItem 一一对应，含导入结果）。
type ImportRecordItem struct {
	ID                  string
	RecordID            string
	DraftItem
	PingCodeID         string
	PingCodeIdentifier string
	Status             string // pending | success | failed
	ErrorMessage       string
}

// 导入记录状态机。
const (
	RecordStatusAnalyzed        = "analyzed"
	RecordStatusImporting       = "importing"
	RecordStatusSuccess         = "success"
	RecordStatusPartialSuccess  = "partial_success"
	RecordStatusFailed          = "failed"
)

// 明细条目状态。
const (
	ItemStatusPending = "pending"
	ItemStatusSuccess = "success"
	ItemStatusFailed  = "failed"
)
