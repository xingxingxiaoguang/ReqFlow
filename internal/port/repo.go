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

// DatasetRepo 数据集仓储：任务产物的落点，也是查重/agent 工具/任务间衔接的底料。
type DatasetRepo interface {
	CreateDataset(ctx context.Context, d *model.Dataset) error
	UpdateDataset(ctx context.Context, d *model.Dataset) error
	ListDatasets(ctx context.Context, typ string, limit int) ([]model.Dataset, error)
	GetDataset(ctx context.Context, id string) (*model.Dataset, error)
	CountDatasets(ctx context.Context, typ string) (int64, error)
	CountDatasetItems(ctx context.Context, typ string) (int64, error)
	// ReplaceDatasetItems 重建数据集条目（含向量；未发布数据集断点续跑 = 全量重写）。
	ReplaceDatasetItems(ctx context.Context, datasetID string, items []DatasetItemVector) error
	// ListDatasetItemsByType 某类型全部条目（查重/工具语料，跨数据集）。
	ListDatasetItemsByType(ctx context.Context, typ string) ([]model.DatasetItem, error)
	// ListDatasetItems 单数据集条目（浏览）。
	ListDatasetItems(ctx context.Context, datasetID string, limit int) ([]model.DatasetItem, error)
	// SearchSimilarDatasetItems 语义检索某类型条目（查重/关联匹配）。
	SearchSimilarDatasetItems(ctx context.Context, vec []float32, typ string, n int) ([]SimilarDatasetItem, error)
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
