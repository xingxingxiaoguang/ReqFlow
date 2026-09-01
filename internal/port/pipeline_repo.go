package port

import (
	"context"
	"errors"

	"reqflow/internal/domain/model"
)

var (
	ErrDatasetItemKeyConflict  = errors.New("dataset item key conflict")
	ErrDatasetBatchNotWritable = errors.New("dataset batch is not writable")
	ErrPipelineCursorConflict  = errors.New("pipeline cursor conflict")
)

// DatasetPipelineRepo 是 V2 不可变 Schema 与追加型 Dataset 的持久化边界。
type DatasetPipelineRepo interface {
	CreateDatasetSchema(ctx context.Context, schema *model.DatasetSchemaDefinition) error
	GetDatasetSchema(ctx context.Context, id string) (*model.DatasetSchemaDefinition, error)

	CreateAppendDataset(ctx context.Context, dataset *model.Dataset) error
	GetAppendDataset(ctx context.Context, id string) (*model.Dataset, error)

	CreateDatasetBatch(ctx context.Context, batch *model.DatasetBatch) error
	GetOrCreateDatasetBatchForNode(ctx context.Context, batch *model.DatasetBatch, producerAttempt int) (*model.DatasetBatch, error)
	GetDatasetBatch(ctx context.Context, id string) (*model.DatasetBatch, error)
	CommitDatasetBatch(ctx context.Context, batchID, payloadHash string, items []model.DatasetItem) (*model.DatasetBatch, error)
	CommitDatasetBatchForNode(ctx context.Context, batchID, producerNodeRunID string, producerAttempt int,
		payloadHash string, items []model.DatasetItem) (*model.DatasetBatch, error)
	ListDatasetItemsAfter(ctx context.Context, datasetID string, afterSeq, throughSeq int64, limit int) ([]model.DatasetItem, error)
}

// QueryDatasetPipelineRepo 为 Base Dataset → Query Dataset 的增量派生提供专用事务边界。
// Batch 提交与 Cursor 推进必须由同一实现原子完成，应用层禁止拆成两个调用。
type QueryDatasetPipelineRepo interface {
	GetAppendDataset(ctx context.Context, id string) (*model.Dataset, error)
	GetDatasetSchema(ctx context.Context, id string) (*model.DatasetSchemaDefinition, error)
	ListDatasetItemsAfter(ctx context.Context, datasetID string, afterSeq, throughSeq int64, limit int) ([]model.DatasetItem, error)
	GetOrCreateDatasetBatchForNode(ctx context.Context, batch *model.DatasetBatch, producerAttempt int) (*model.DatasetBatch, error)

	GetPipelineCursor(ctx context.Context, pipelineKey, sourceDatasetID, targetDatasetID string) (*model.PipelineCursor, error)
	GetOrCreatePipelineCursor(ctx context.Context, pipelineKey, sourceDatasetID, targetDatasetID string) (*model.PipelineCursor, error)
	CommitQueryDatasetBatchForNode(ctx context.Context, batchID, producerNodeRunID string, producerAttempt int,
		payloadHash string, items []model.DatasetItem, cursorID string, expectedThroughSeq, advanceThroughSeq int64,
		lastSuccessTaskID string) (*model.DatasetBatch, *model.PipelineCursor, error)
}
