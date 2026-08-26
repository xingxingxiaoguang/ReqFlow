// Package port 定义业务层（app）与外层（infra）之间的全部出站契约。
// 依赖方向：infra 实现 port，app 消费 port；port 仅依赖 domain。
// 新增外部依赖（换 ORM、接新协作平台、换 LLM 协议）只允许新增 infra 实现，契约不动。
package port

import (
	"context"

	"reqflow/internal/domain/model"
)

/* ---- 仓储 ---- */

// DatasetItemVector 数据集条目 + 向量（写入侧）。向量由 app 调 Embedder 生成后传入，
// domain 实体不含向量，保持领域纯净。
type DatasetItemVector struct {
	model.DatasetItem
	Embedding []float32
}

// SimilarDatasetItem 语义检索命中的数据集条目。
type SimilarDatasetItem struct {
	model.DatasetItem
	Distance float64 // 余弦距离 0-2，越小越相似；由用例层换算为 0-1 分数
}

// UpsertMode 幂等写入的冲突处理方式。
type UpsertMode string

const (
	// UpsertInsertMissing 只插入新条目，已存在的 key 跳过（merge 策略）。
	UpsertInsertMissing UpsertMode = "insert_missing"
	// UpsertUpdateExisting 新条目插入，已存在的 key 更新（upsert 策略）。
	UpsertUpdateExisting UpsertMode = "update_existing"
)

// DatasetItemKeyInfo 预览分桶所需的既有条目身份索引。
type DatasetItemKeyInfo struct {
	ID           string
	Fingerprint  string
	SourceTaskID string
}

// FieldCondition 字段下推过滤（fields JSON 内的 filterable 字段）。
type FieldCondition struct {
	Field string // schema 字段 key
	Op    string // eq | ne | in | contains
	Value any    // in 为 []string，其余为 string
}

// ItemFilter 条目查询范围与过滤（浏览筛选 / 语义检索前过滤）。
type ItemFilter struct {
	DatasetID string // 空 = 按 Type 跨数据集
	Type      string
	Conds     []FieldCondition
	Limit     int
}

// DatasetRepo 数据集仓储：任务产物的落点，也是查重/agent 工具/任务间衔接的底料。
type DatasetRepo interface {
	CreateDataset(ctx context.Context, d *model.Dataset) error
	UpdateDataset(ctx context.Context, d *model.Dataset) error
	ListDatasets(ctx context.Context, typ string, limit int) ([]model.Dataset, error)
	GetDataset(ctx context.Context, id string) (*model.Dataset, error)
	CountDatasets(ctx context.Context, typ string) (int64, error)
	CountDatasetItems(ctx context.Context, typ string) (int64, error)
	// ReplaceDatasetItems 重建数据集条目（含向量；新建数据集断点续跑 = 全量重写）。
	ReplaceDatasetItems(ctx context.Context, datasetID string, items []DatasetItemVector) error
	// UpsertDatasetItems 幂等写入：按 (dataset_id, item_key) 冲突处理（mode 决定跳过或更新）。
	UpsertDatasetItems(ctx context.Context, datasetID, sourceTaskID string, items []DatasetItemVector, mode UpsertMode) error
	// DeleteDatasetItemsBySource 删除指定任务写入的条目（replace 策略的前置清理）。
	DeleteDatasetItemsBySource(ctx context.Context, datasetID, sourceTaskID string) (int64, error)
	// GetDatasetItemKeyMap 取数据集内 item_key → 身份索引（预览分桶：判定插入/更新/无变化）。
	GetDatasetItemKeyMap(ctx context.Context, datasetID string) (map[string]DatasetItemKeyInfo, error)
	// CountDatasetItemsOfDataset 单数据集条目数（写入后回填 item_count）。
	CountDatasetItemsOfDataset(ctx context.Context, datasetID string) (int64, error)
	// ListDatasetItemsByType 某类型全部条目（查重/工具语料，跨数据集）。
	ListDatasetItemsByType(ctx context.Context, typ string) ([]model.DatasetItem, error)
	// ListDatasetItems 单数据集条目（浏览）。
	ListDatasetItems(ctx context.Context, datasetID string, limit int) ([]model.DatasetItem, error)
	// ListDatasetItemsFiltered 条件查询：字段过滤下推（fields JSON 的 filterable 字段）。
	ListDatasetItemsFiltered(ctx context.Context, f ItemFilter) ([]model.DatasetItem, error)
	// SearchSimilarDatasetItems 语义检索某类型条目（查重/关联匹配）。
	SearchSimilarDatasetItems(ctx context.Context, vec []float32, typ string, n int) ([]SimilarDatasetItem, error)
	// SearchSimilarDatasetItemsFiltered 语义检索 + 范围/字段过滤（统一查询）。
	SearchSimilarDatasetItemsFiltered(ctx context.Context, vec []float32, f ItemFilter, n int) ([]SimilarDatasetItem, error)
}

// TaskFilter 任务列表查询。
type TaskFilter struct {
	Status string // 空 = 全部
	Type   string // 空 = 全部
	Limit  int
}

// TaskRepo 任务与步骤/明细仓储。
type TaskRepo interface {
	CreateTask(ctx context.Context, t *model.Task) error
	UpdateTask(ctx context.Context, t *model.Task) error
	ListTasks(ctx context.Context, f TaskFilter) ([]model.Task, error)
	CountTasks(ctx context.Context) (int64, error)
	GetTask(ctx context.Context, id string) (*model.Task, error)
	// CreateTaskSteps 播种步骤（任务创建时）。
	CreateTaskSteps(ctx context.Context, taskID string, steps []model.TaskStep) error
	GetTaskSteps(ctx context.Context, taskID string) ([]model.TaskStep, error)
	UpdateTaskStep(ctx context.Context, step *model.TaskStep) error
	GetTaskItems(ctx context.Context, taskID string) ([]model.TaskItem, error)
	// ReplaceTaskItems 批量替换草稿（仅替换无结果的行——已落库的保留）。
	ReplaceTaskItems(ctx context.Context, taskID string, items []model.TaskItem) error
	// UpdateItemResult 单条明细状态回写。
	UpdateItemResult(ctx context.Context, itemID, status, errMsg string) error
	// RecoverStuck 启动恢复：把卡在 running 的任务/步骤标为 paused（服务重启中断）。
	RecoverStuck(ctx context.Context) error
}

// TaskStepsSnapshot 任务步骤/明细快照（归档行内嵌 JSON 的载荷形状）。
type TaskStepsSnapshot struct {
	Steps []model.TaskStep `json:"steps"`
	Items []model.TaskItem `json:"items"`
}

// ArchiveRepo 归档仓储：删除入档与恢复（同一事务内完成搬移，主表与归档表互斥）。
type ArchiveRepo interface {
	// ArchiveTask 任务行 SQL 直搬入归档（含 steps/items 快照）并删主表数据。
	ArchiveTask(ctx context.Context, task *model.Task, snap TaskStepsSnapshot) error
	// RestoreTask 归档任务回主表（含快照写回；返回完整任务数据）。
	RestoreTask(ctx context.Context, taskID string) (*model.Task, TaskStepsSnapshot, error)
	// ListArchivedTasks 归档任务列表（typ 空 = 全部）。
	ListArchivedTasks(ctx context.Context, typ string, limit int) ([]model.ArchivedTask, error)
	// ArchiveDataset 数据集 + 条目（含向量）SQL 直搬入归档并删主表数据。
	ArchiveDataset(ctx context.Context, datasetID string) error
	// RestoreDataset 归档数据集 + 条目回主表。
	RestoreDataset(ctx context.Context, datasetID string) error
	// ListArchivedDatasets 归档数据集列表（typ 空 = 全部）。
	ListArchivedDatasets(ctx context.Context, typ string, limit int) ([]model.ArchivedDataset, error)
}
