package port

import (
	"context"

	"reqflow/internal/domain/model"
)

// RecordTransformRepo 持久化 data.transform 的恢复真相。每条记录完成后独立落库，
// 重试同一 StepRun 时只补齐缺失记录。
type RecordTransformRepo interface {
	BeginTransformedRecordSet(ctx context.Context, set *model.TransformedRecordSet) (*model.TransformedRecordSet, error)
	GetTransformedRecordSet(ctx context.Context, id string) (*model.TransformedRecordSet, error)
	GetTransformedRecordSetByStepRun(ctx context.Context, stepRunID string) (*model.TransformedRecordSet, error)
	SaveTransformedRecord(ctx context.Context, setID string, producerAttempt int, record *model.TransformedRecord) error
	FinalizeTransformedRecordSet(ctx context.Context, setID string, producerAttempt int) (*model.TransformedRecordSet, error)
	ListTransformedRecords(ctx context.Context, setID string) ([]model.TransformedRecord, error)
}

// RecordValidationRepo 持久化固定 Dataset through_seq 下的校验结果。已有 key 查询
// 必须受 through_seq 限制，使一次审核看到的冲突快照可重复重建。
type RecordValidationRepo interface {
	BeginValidationResultSet(ctx context.Context, set *model.ValidationResultSet) (*model.ValidationResultSet, error)
	GetValidationResultSet(ctx context.Context, id string) (*model.ValidationResultSet, error)
	GetValidationResultSetByStepRun(ctx context.Context, stepRunID string) (*model.ValidationResultSet, error)
	SaveValidationResult(ctx context.Context, setID string, producerAttempt int, result *model.ValidationResult) error
	FinalizeValidationResultSet(ctx context.Context, setID string, producerAttempt int) (*model.ValidationResultSet, error)
	ListValidationResults(ctx context.Context, setID string) ([]model.ValidationResult, error)
	FindExistingDatasetItemKeys(ctx context.Context, datasetID string, throughSeq int64, itemKeys []string) (map[string]struct{}, error)
}

type CleaningPipelineRepo interface {
	ExtractionProfileRepo
	RecordDraftRepo
	DatasetPipelineRepo
	RecordTransformRepo
	RecordValidationRepo
}
