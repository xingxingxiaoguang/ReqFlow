// Package port 定义业务层（app）与外层（infra）之间的全部出站契约。
// 依赖方向：infra 实现 port，app 消费 port；port 仅依赖 domain。
// 新增外部依赖（换 ORM、接新协作平台、换 LLM 协议）只允许新增 infra 实现，契约不动。
package port

import (
	"context"

	"reqflow/internal/domain/model"
)

/* ---- 仓储 ---- */

// ProjectVector 项目 + 向量（写入侧）。向量由 app 调 Embedder 生成后传入，
// domain 实体不含向量，保持领域纯净。
type ProjectVector struct {
	model.Project
	Embedding []float32
}

// WorkItemVector 工作项 + 向量（写入侧）。
type WorkItemVector struct {
	model.WorkItem
	Embedding []float32
}

// SimilarProject 语义检索命中的项目。
type SimilarProject struct {
	model.Project
	Distance float64 // 余弦距离 0-2，越小越相似；由用例层换算为 0-1 分数
}

// SimilarWorkItem 语义检索命中的工作项。
type SimilarWorkItem struct {
	model.WorkItem
	Distance float64
}

// ProjectRepo 项目同步缓存仓储。
type ProjectRepo interface {
	UpsertWithVectors(ctx context.Context, items []ProjectVector) error // 含归档项恢复（is_archived=false）
	ListAll(ctx context.Context) ([]model.Project, error)              // 含归档（增量比对用）
	ListActive(ctx context.Context) ([]model.Project, error)
	Archive(ctx context.Context, ids []string) error
	SearchSimilar(ctx context.Context, vec []float32, n int) ([]SimilarProject, error)
	CountActive(ctx context.Context) (int64, error)
}

// WorkItemFilter 工作项浏览查询。
type WorkItemFilter struct {
	ProjectID string
	Search    string // 标题/编号模糊
	Limit     int
	Offset    int
}

// WorkItemRepo 工作项同步缓存仓储（需求语料库）。
type WorkItemRepo interface {
	UpsertWithVectors(ctx context.Context, items []WorkItemVector) error
	ListAll(ctx context.Context) ([]model.WorkItem, error) // 含归档（增量比对用）
	ListActive(ctx context.Context, filter WorkItemFilter) ([]model.WorkItem, int64, error)
	Archive(ctx context.Context, ids []string) error
	// SearchSimilar 语义检索；projectID 非空时限定项目范围（查重用）
	SearchSimilar(ctx context.Context, vec []float32, projectID string, n int) ([]SimilarWorkItem, error)
	CountActive(ctx context.Context) (int64, error)
}

// MetaRepo 平台元数据缓存（名称 → UUID 映射）。
type MetaRepo interface {
	UpsertTypes(ctx context.Context, types []model.MetaType) error
	UpsertStates(ctx context.Context, states []model.MetaState) error
	UpsertPriorities(ctx context.Context, priorities []model.MetaPriority) error
	ListTypes(ctx context.Context, projectID string) ([]model.MetaType, error)
	ListStates(ctx context.Context, projectID string) ([]model.MetaState, error)
	ListPriorities(ctx context.Context, projectID string) ([]model.MetaPriority, error)
}

// ImportRepo 分析记录与导入明细仓储。
type ImportRepo interface {
	CreateRecord(ctx context.Context, rec *model.ImportRecord) error
	UpdateRecord(ctx context.Context, rec *model.ImportRecord) error
	ListRecords(ctx context.Context, limit int) ([]model.ImportRecord, error)
	GetRecord(ctx context.Context, id string) (*model.ImportRecord, error)
	GetRecordItems(ctx context.Context, recordID string) ([]model.ImportRecordItem, error)
	ReplaceRecordItems(ctx context.Context, recordID string, items []model.ImportRecordItem) error
	// UpdateItemResult 导入完成回写单条明细结果
	UpdateItemResult(ctx context.Context, itemID, pingcodeID, identifier, status, errMsg string) error
}
