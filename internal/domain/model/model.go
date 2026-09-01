// Package model 定义 ReqFlow 的领域实体与值。
// 本包仅依赖标准库；V2 核心三态：Dataset（追加型数据容器）、DatasetItem（不可变
// 条目）、Task（从 TaskDefinition 派生的执行实例，冻结定义快照与资源边界）。
package model

import "time"

/* ---- Dataset 与条目（不可变 Schema + 只追加 Batch） ---- */

// Dataset 长期数据容器：Schema 与 key_fields 创建后固定，数据经不可变
// DatasetBatch 追加；CurrentSeq 是最近一次 Batch 原子提交的位点。
type Dataset struct {
	ID          string
	WorkspaceID string
	Purpose     DatasetPurpose // base / query / analysis / graph_node / graph_edge（目录分类）
	Name        string
	Description string
	SchemaID    string // 不可变 DatasetSchemaDefinition；结构变化 = 新 Schema + 新 Dataset
	KeyFields   []string
	Status      string // active | sealed | archived
	ItemCount   int
	CurrentSeq  int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// DatasetItem 已提交条目：不 UPDATE/DELETE；身份 = ItemKey（key_fields 归一化拼接），
// Fingerprint 为规范化 fields 的稳定哈希；Provenance 保留 Batch/Task/Step/Asset/Block 链路。
type DatasetItem struct {
	ID          string
	DatasetID   string
	BatchID     string
	Fields      string // JSON 文本（受 Schema 校验的字段袋）
	ItemKey     string
	Fingerprint string
	CommitSeq   int64 // Dataset 内连续递增，仅 Batch 原子提交时分配
	Provenance  string
	CreatedAt   time.Time
}

/* ---- Task（执行实例） ---- */

// Task 从一个 active TaskDefinition 派生的一次执行：创建时冻结 DefinitionSnapshot
// 与资源绑定，运行状态由 StepRun 承载（本结构只保留任务级生命周期）。
// BatchID/BatchOrdinal/BatchSize/SourceAssetID/SourceFilename 来自 TaskBatchService
// 的批量派生来源；单任务派生时为零值。
type Task struct {
	ID                 string
	WorkspaceID        string
	DefinitionID       string
	DefinitionSnapshot string // JSON 文本：创建时的完整定义（步骤 DAG + 端口 + Executor 配置）
	Type               string // 来源 Definition 的 key（目录展示用）
	Title              string
	BatchID            string
	BatchOrdinal       int
	BatchSize          int
	SourceAssetID      string
	SourceFilename     string
	Status             string
	ErrorMessage       string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	StartedAt          time.Time // 零值 = 未开始
	FinishedAt         time.Time // 零值 = 未终态
}

// 任务状态机（StepRun 状态见 task_definition.go；本组只描述任务级生命周期）。
const (
	TaskStatusPending   = "pending"
	TaskStatusRunning   = "running"
	TaskStatusPausing   = "pausing"  // 已发出暂停请求，等待持有 lease 的 Worker 落检查点
	TaskStatusAwaiting  = "awaiting" // 等待人工操作（Human Gate）
	TaskStatusPaused    = "paused"   // 用户暂停 / 服务重启中断
	TaskStatusSucceeded = "succeeded"
	TaskStatusFailed    = "failed"
)
