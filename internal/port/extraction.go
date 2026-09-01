package port

import (
	"context"

	"reqflow/internal/domain/model"
)

type RecordDraftRepo interface {
	BeginRecordDraftSet(ctx context.Context, set *model.RecordDraftSet, units []model.ExtractionUnit) (*model.RecordDraftSet, error)
	GetRecordDraftSet(ctx context.Context, id string) (*model.RecordDraftSet, []model.ExtractionUnit, error)
	GetRecordDraftSetByNodeRun(ctx context.Context, nodeRunID string) (*model.RecordDraftSet, []model.ExtractionUnit, error)
	StartExtractionUnit(ctx context.Context, setID string, producerAttempt int, unitKey string) error
	CompleteExtractionUnit(ctx context.Context, setID string, producerAttempt int, unitKey, responseHash string, usage model.LLMUsage, drafts []model.RecordDraft) error
	FailExtractionUnit(ctx context.Context, setID string, producerAttempt int, unitKey, message string, usage model.LLMUsage) error
	FinalizeRecordDraftSet(ctx context.Context, setID string, producerAttempt int) (*model.RecordDraftSet, error)
	ListRecordDrafts(ctx context.Context, setID string) ([]model.RecordDraft, error)
}

type ExtractionPipelineRepo interface {
	RecordDraftRepo
	AssetCatalogRepo
	ParsedDocumentRepo
	DatasetPipelineRepo
}
