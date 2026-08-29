package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"reqflow/internal/domain/model"
)

type TransformedRecordSetView struct {
	ID                  string                  `json:"id"`
	RecordDraftSetID    string                  `json:"record_draft_set_id"`
	ExtractionProfileID string                  `json:"extraction_profile_id"`
	TargetSchemaID      string                  `json:"target_schema_id"`
	SourceStepRunID     string                  `json:"source_step_run_id"`
	Status              string                  `json:"status"`
	EngineVersion       string                  `json:"engine_version"`
	DraftCount          int                     `json:"draft_count"`
	TransformedCount    int                     `json:"transformed_count"`
	ChangedRecordCount  int                     `json:"changed_record_count"`
	IssueCount          int                     `json:"issue_count"`
	CreatedAt           time.Time               `json:"created_at"`
	FinishedAt          time.Time               `json:"finished_at,omitempty"`
	Records             []TransformedRecordView `json:"records"`
}

type TransformedRecordView struct {
	ID            string               `json:"id"`
	RecordDraftID string               `json:"record_draft_id"`
	Ordinal       int                  `json:"ordinal"`
	Fields        json.RawMessage      `json:"fields"`
	Changes       []model.RecordChange `json:"changes"`
	Issues        []model.RecordIssue  `json:"issues"`
	CreatedAt     time.Time            `json:"created_at"`
}

func (s *CleaningService) TransformedRecordSetView(ctx context.Context, id string) (*TransformedRecordSetView, error) {
	set, records, err := s.GetTransformedRecordSet(ctx, id)
	if err != nil {
		return nil, err
	}
	view := &TransformedRecordSetView{ID: set.ID, RecordDraftSetID: set.RecordDraftSetID,
		ExtractionProfileID: set.ExtractionProfileID, TargetSchemaID: set.TargetSchemaID,
		SourceStepRunID: set.SourceStepRunID, Status: set.Status, EngineVersion: set.EngineVersion,
		DraftCount: set.DraftCount, TransformedCount: set.TransformedCount,
		ChangedRecordCount: set.ChangedRecordCount, IssueCount: set.IssueCount,
		CreatedAt: set.CreatedAt, FinishedAt: set.FinishedAt,
		Records: make([]TransformedRecordView, len(records))}
	for i, record := range records {
		view.Records[i] = TransformedRecordView{ID: record.ID, RecordDraftID: record.RecordDraftID,
			Ordinal: record.Ordinal, Fields: record.Fields, Changes: record.Changes,
			Issues: record.Issues, CreatedAt: record.CreatedAt}
	}
	return view, nil
}

type ValidationResultSetView struct {
	ID                     string                 `json:"id"`
	TransformedRecordSetID string                 `json:"transformed_record_set_id"`
	TargetDatasetID        string                 `json:"target_dataset_id"`
	TargetSchemaID         string                 `json:"target_schema_id"`
	SourceStepRunID        string                 `json:"source_step_run_id"`
	Status                 string                 `json:"status"`
	EngineVersion          string                 `json:"engine_version"`
	ValidatedThroughSeq    int64                  `json:"validated_through_seq"`
	RecordCount            int                    `json:"record_count"`
	ValidCount             int                    `json:"valid_count"`
	WarningCount           int                    `json:"warning_count"`
	InvalidCount           int                    `json:"invalid_count"`
	DuplicateCount         int                    `json:"duplicate_count"`
	ConflictCount          int                    `json:"conflict_count"`
	CreatedAt              time.Time              `json:"created_at"`
	FinishedAt             time.Time              `json:"finished_at,omitempty"`
	Results                []ValidationResultView `json:"results"`
}

type ValidationResultView struct {
	ID                  string               `json:"id"`
	TransformedRecordID string               `json:"transformed_record_id"`
	Ordinal             int                  `json:"ordinal"`
	DraftFields         json.RawMessage      `json:"draft_fields"`
	Fields              json.RawMessage      `json:"fields"`
	FieldConfidence     json.RawMessage      `json:"field_confidence"`
	Changes             []model.RecordChange `json:"changes"`
	ItemKey             string               `json:"item_key,omitempty"`
	Fingerprint         string               `json:"fingerprint,omitempty"`
	Status              string               `json:"status"`
	Issues              []model.RecordIssue  `json:"issues"`
	Provenance          model.ItemProvenance `json:"provenance"`
	CreatedAt           time.Time            `json:"created_at"`
}

func (s *CleaningService) ValidationResultSetView(ctx context.Context, id string) (*ValidationResultSetView, error) {
	set, results, err := s.GetValidationResultSet(ctx, id)
	if err != nil {
		return nil, err
	}
	transformedSet, err := s.repo.GetTransformedRecordSet(ctx, set.TransformedRecordSetID)
	if err != nil {
		return nil, fmt.Errorf("读取 ValidationResultSet 的转换来源: %w", err)
	}
	transformedRecords, err := s.repo.ListTransformedRecords(ctx, transformedSet.ID)
	if err != nil {
		return nil, fmt.Errorf("读取 ValidationResultSet 的转换记录: %w", err)
	}
	drafts, err := s.repo.ListRecordDrafts(ctx, transformedSet.RecordDraftSetID)
	if err != nil {
		return nil, fmt.Errorf("读取 ValidationResultSet 的候选来源: %w", err)
	}
	recordByID := make(map[string]model.TransformedRecord, len(transformedRecords))
	for _, record := range transformedRecords {
		recordByID[record.ID] = record
	}
	draftByID := make(map[string]model.RecordDraft, len(drafts))
	for _, draft := range drafts {
		draftByID[draft.ID] = draft
	}
	view := &ValidationResultSetView{ID: set.ID, TransformedRecordSetID: set.TransformedRecordSetID,
		TargetDatasetID: set.TargetDatasetID, TargetSchemaID: set.TargetSchemaID,
		SourceStepRunID: set.SourceStepRunID, Status: set.Status, EngineVersion: set.EngineVersion,
		ValidatedThroughSeq: set.ValidatedThroughSeq, RecordCount: set.RecordCount,
		ValidCount: set.ValidCount, WarningCount: set.WarningCount, InvalidCount: set.InvalidCount,
		DuplicateCount: set.DuplicateCount, ConflictCount: set.ConflictCount,
		CreatedAt: set.CreatedAt, FinishedAt: set.FinishedAt,
		Results: make([]ValidationResultView, len(results))}
	for i, result := range results {
		record, ok := recordByID[result.TransformedRecordID]
		if !ok {
			return nil, fmt.Errorf("ValidationResult %s 缺少 TransformedRecord 来源", result.ID)
		}
		draft, ok := draftByID[record.RecordDraftID]
		if !ok {
			return nil, fmt.Errorf("TransformedRecord %s 缺少 RecordDraft 来源", record.ID)
		}
		view.Results[i] = ValidationResultView{ID: result.ID, TransformedRecordID: result.TransformedRecordID,
			Ordinal: result.Ordinal, DraftFields: draft.Fields, Fields: result.Fields,
			FieldConfidence: draft.FieldConfidence, Changes: record.Changes, ItemKey: result.ItemKey,
			Fingerprint: result.Fingerprint, Status: result.Status,
			Issues: result.Issues, Provenance: draft.Provenance, CreatedAt: result.CreatedAt}
	}
	return view, nil
}
