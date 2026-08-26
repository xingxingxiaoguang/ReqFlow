// Package model 定义 ReqFlow 的领域实体与值。
// 本包仅依赖标准库，承载数据集、分析草稿、任务三块核心状态。
package model

import "time"

/* ---- 数据集（任务产出的结果集，任务间衔接的载体） ---- */

// Dataset 结果集：任务（如需求导入）产出的结构化数据集合，
// 也是后续任务（如 bug 分析）的输入底料——任务 + 数据驱动的业务闭环。
type Dataset struct {
	ID            string
	Type          string // requirement | bug | …（对应 DatasetSchema.Type）
	Name          string
	Description   string // 人类可读说明（列表页展示）
	Tags          []string
	SourceTaskID  string // 产生该数据集的任务（可选；merge/upsert 写入不改变来源）
	Status        string // ready | building（写入中，未发布）
	ItemCount     int
	SchemaVersion int // 创建时 schema 版本（演进依据）
	Extra         string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// DatasetItem 数据集条目（fields 为 schema 类型化字段的 JSON 文本）。
// ItemKey 为条目业务主键（schema KeyFields 归一化拼接，upsert/去重基准）；
// Fingerprint 为内容哈希（相同则跳过更新与重嵌）；均为空表示迁移前的存量条目。
type DatasetItem struct {
	ID           string
	DatasetID    string
	Fields       string // JSON 文本
	ItemKey      string
	Fingerprint  string
	SourceTaskID string
	UpdatedAt    time.Time
	CreatedAt    time.Time
}

// 数据集类型。
const (
	DatasetTypeRequirement = "requirement"
	DatasetTypeBug         = "bug"
)

// 数据集状态。
const (
	DatasetStatusReady    = "ready"
	DatasetStatusBuilding = "building"
)

/* ---- 分析草稿与任务 ---- */

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

// Task 一轮长程流程（需求导入/bug…）的生命周期载体。
// 状态机：pending → running → awaiting(人工门) | paused → running → succeeded | failed（终态）。
type Task struct {
	ID                string
	Type              string // requirement_import | bug_*（第二波）
	Title             string
	Status            string
	CurrentStep       int // 当前步骤序号（0=未开始；与 TaskStep.Seq 对应）
	Workflow          string // 工作流定义快照（JSON 文本：步骤链 + 依赖声明，创建时从注册表写入）
	Input             string // JSON 文本：文件信息/解析文本/附加要求
	Output            string // JSON 文本：统计/数据集引用等
	// AgentContext 分析会话的 JSON 序列化（port.Context：系统提示 + 消息序列 + 工具表）。
	// 暂停时落库、继续时回放——换模型续跑与 refine 微调的统一载体；空 = 未记录。
	AgentContext      string
	ItemsCount        int
	ImportedCount     int
	FailedCount       int
	TargetProjectID   string
	TargetProjectName string
	// OutputDatasetID 本任务产出的数据集（如需求导入 → 需求数据集）。
	OutputDatasetID string
	// InputDatasetID 本任务消费的数据集（如 bug 分析 → 需求数据集，关联匹配底料）。
	InputDatasetID string
	ErrorMessage   string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	StartedAt         time.Time // 零值 = 未开始
	FinishedAt        time.Time // 零值 = 未终态
}

// TaskStep 任务步骤（执行轨迹，逐条落库供详情页时间线与重放）。
type TaskStep struct {
	ID        string
	TaskID    string
	Seq       int
	Name      string
	Status    string // pending | running | succeeded | failed | awaiting
	Detail    string // 最新进度消息
	Data      string // JSON 文本：工具轨迹/导入汇总等
	StartedAt time.Time
	EndedAt   time.Time
}

// TaskItem 任务明细草稿（AI 分析产物，生成数据集前的编辑缓冲）。
type TaskItem struct {
	ID           string
	TaskID       string
	DraftItem
	Status       string // pending | success | failed
	ErrorMessage string
}

// 任务类型。
const (
	TaskTypeRequirementImport = "requirement_import"
)

/* ---- 工作流元数据（半元数据驱动：定义即数据，执行器按 StepKind 注册分发） ---- */

// StepKind 步骤执行类型（执行器注册表键；human 为纯人工门，无执行器）。
type StepKind string

const (
	StepKindParse    StepKind = "parse"    // 文档解析（DocParser）
	StepKindHuman    StepKind = "human"    // 人工确认门（无执行器，等待人工操作）
	StepKindAnalyze  StepKind = "analyze"  // AI agent 分析（agent loop + 任务专属工具）
	StepKindDataset  StepKind = "dataset"  // 生成数据集（向量化写入，任务产出）
)

// StepDependency 步骤依赖声明（元数据展示用：步骤依赖什么数据与工具）。
type StepDependency struct {
	Data string `json:"data"` // 数据依赖：task.input 字段 / 前序步骤产物（file/parsed_text/items/project…）
	Tool string `json:"tool"` // 工具依赖：doc_parser / human / agent_loop(工具清单) / dataset_writer / embedder…
}

// WorkflowStep 工作流步骤定义（元数据）。
type WorkflowStep struct {
	Seq  int              `json:"seq"`
	Name string           `json:"name"`
	Kind StepKind         `json:"kind"`
	Deps []StepDependency `json:"deps"`
}

// Workflow 任务类型的工作流定义。创建任务时快照进 tasks.workflow（任务自描述，
// 不受定义演进影响）；执行引擎按 Step.Kind 查找注册的执行器。
type Workflow struct {
	Type  string         `json:"type"`
	Name  string         `json:"name"`
	Desc  string         `json:"desc"`
	Steps []WorkflowStep `json:"steps"`
}

// 任务状态机。
const (
	TaskStatusPending   = "pending"
	TaskStatusRunning   = "running"
	TaskStatusAwaiting  = "awaiting" // 等待人工操作（确认门）
	TaskStatusPaused    = "paused"   // 用户暂停 / 服务重启中断
	TaskStatusSucceeded = "succeeded"
	TaskStatusFailed    = "failed"
)

// 步骤状态。
const (
	StepStatusPending   = "pending"
	StepStatusRunning   = "running"
	StepStatusSucceeded = "succeeded"
	StepStatusFailed    = "failed"
	StepStatusAwaiting  = "awaiting"
	StepStatusPaused    = "paused"
)

// 明细条目状态。
const (
	ItemStatusPending = "pending"
	ItemStatusSuccess = "success"
	ItemStatusFailed  = "failed"
)
