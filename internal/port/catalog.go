package port

import (
	"context"

	"reqflow/internal/domain/model"
)

// V2CatalogRepo 是无代码控制台的只读目录与显式归档边界。运行时写服务不依赖它，
// 避免为了管理页面扩大 Worker/Executor 的接口。
type V2CatalogRepo interface {
	ListTaskDefinitions(ctx context.Context, workspaceID, status string, limit int) ([]model.TaskDefinition, error)
	ListDatasetSchemas(ctx context.Context, workspaceID string, limit int) ([]model.DatasetSchemaDefinition, error)
	ListAppendDatasets(ctx context.Context, workspaceID, status string, purpose model.DatasetPurpose, limit int) ([]model.Dataset, error)
	ListDatasetBatches(ctx context.Context, datasetID string, limit int) ([]model.DatasetBatch, error)
	SetAppendDatasetStatus(ctx context.Context, datasetID, fromStatus, toStatus string) error
	ListAssetSets(ctx context.Context, workspaceID string, limit int) ([]model.AssetSet, error)
	ListExtractionProfiles(ctx context.Context, workspaceID string, limit int) ([]model.ExtractionProfile, error)
	ArchiveOrchestratorTask(ctx context.Context, taskID string) error
	RestoreOrchestratorTask(ctx context.Context, taskID string) error
	ListArchivedOrchestratorTasks(ctx context.Context, workspaceID string, limit int) ([]model.Task, error)
}
