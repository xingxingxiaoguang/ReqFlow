package port

import (
	"context"

	"reqflow/internal/domain/model"
)

type ExtractionProfileRepo interface {
	CreateExtractionProfile(ctx context.Context, profile *model.ExtractionProfile) error
	GetExtractionProfile(ctx context.Context, id string) (*model.ExtractionProfile, error)
}

type RecordDraftRepo interface {
	BeginRecordDraftSet(ctx context.Context, set *model.RecordDraftSet, units []model.ExtractionUnit) (*model.RecordDraftSet, error)
	GetRecordDraftSet(ctx context.Context, id string) (*model.RecordDraftSet, []model.ExtractionUnit, error)
	GetRecordDraftSetByStepRun(ctx context.Context, stepRunID string) (*model.RecordDraftSet, []model.ExtractionUnit, error)
	StartExtractionUnit(ctx context.Context, setID string, producerAttempt int, unitKey string) error
	CompleteExtractionUnit(ctx context.Context, setID string, producerAttempt int, unitKey, responseHash string, usage model.LLMUsage, drafts []model.RecordDraft) error
	FailExtractionUnit(ctx context.Context, setID string, producerAttempt int, unitKey, message string, usage model.LLMUsage) error
	FinalizeRecordDraftSet(ctx context.Context, setID string, producerAttempt int) (*model.RecordDraftSet, error)
	ListRecordDrafts(ctx context.Context, setID string) ([]model.RecordDraft, error)
}

type ExtractionPipelineRepo interface {
	ExtractionProfileRepo
	RecordDraftRepo
	AssetCatalogRepo
	ParsedDocumentRepo
	DatasetPipelineRepo
}
