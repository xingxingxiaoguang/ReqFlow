package port

import (
	"context"
	"errors"

	"reqflow/internal/domain/model"
)

var (
	ErrDatasetItemKeyConflict  = errors.New("dataset item key conflict")
	ErrDatasetBatchNotWritable = errors.New("dataset batch is not writable")
)

// DatasetPipelineRepo 是 V2 不可变 Schema 与追加型 Dataset 的持久化边界。
type DatasetPipelineRepo interface {
	CreateDatasetSchema(ctx context.Context, schema *model.DatasetSchemaDefinition) error
	GetDatasetSchema(ctx context.Context, id string) (*model.DatasetSchemaDefinition, error)

	CreateAppendDataset(ctx context.Context, dataset *model.Dataset) error
	GetAppendDataset(ctx context.Context, id string) (*model.Dataset, error)

	CreateDatasetBatch(ctx context.Context, batch *model.DatasetBatch) error
	GetDatasetBatch(ctx context.Context, id string) (*model.DatasetBatch, error)
	CommitDatasetBatch(ctx context.Context, batchID, payloadHash string, items []model.DatasetItem) (*model.DatasetBatch, error)
	ListDatasetItemsAfter(ctx context.Context, datasetID string, afterSeq, throughSeq int64, limit int) ([]model.DatasetItem, error)
}
