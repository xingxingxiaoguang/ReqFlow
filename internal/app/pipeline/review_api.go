package pipeline

import (
	"context"
	"encoding/json"
	"time"

	"reqflow/internal/domain/model"
)

type ApprovedRecordSetView struct {
	ID                    string                     `json:"id"`
	ValidationResultSetID string                     `json:"validation_result_set_id"`
	TargetDatasetID       string                     `json:"target_dataset_id"`
	TargetSchemaID        string                     `json:"target_schema_id"`
	SourceStepRunID       string                     `json:"source_step_run_id"`
	Reviewer              string                     `json:"reviewer"`
	Rationale             string                     `json:"rationale"`
	ReviewHash            string                     `json:"review_hash"`
	ReviewedThroughSeq    int64                      `json:"reviewed_through_seq"`
	RecordCount           int                        `json:"record_count"`
	ApprovedCount         int                        `json:"approved_count"`
	EditedCount           int                        `json:"edited_count"`
	ExcludedCount         int                        `json:"excluded_count"`
	CreatedAt             time.Time                  `json:"created_at"`
	Decisions             []RecordReviewDecisionView `json:"decisions"`
}

type RecordReviewDecisionView struct {
	ID                  string               `json:"id"`
	ValidationResultID  string               `json:"validation_result_id"`
	TransformedRecordID string               `json:"transformed_record_id"`
	Ordinal             int                  `json:"ordinal"`
	Action              string               `json:"action"`
	Fields              json.RawMessage      `json:"fields"`
	ItemKey             string               `json:"item_key,omitempty"`
	Fingerprint         string               `json:"fingerprint,omitempty"`
	Issues              []model.RecordIssue  `json:"issues"`
	Provenance          model.ItemProvenance `json:"provenance"`
	Note                string               `json:"note,omitempty"`
	CreatedAt           time.Time            `json:"created_at"`
}

func (s *ReviewService) ReviewRecords(ctx context.Context, taskID, stepID string,
	input ReviewRecordsInput) (*ApprovedRecordSetView, error) {
	set, err := s.Review(ctx, taskID, stepID, input)
	if err != nil {
		return nil, err
	}
	return s.approvedRecordSetView(ctx, set)
}

func (s *ReviewService) ApprovedRecordSetView(ctx context.Context, id string) (*ApprovedRecordSetView, error) {
	set, err := s.repo.GetApprovedRecordSet(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.approvedRecordSetView(ctx, set)
}

func (s *ReviewService) approvedRecordSetView(ctx context.Context, set *model.ApprovedRecordSet) (*ApprovedRecordSetView, error) {
	decisions, err := s.repo.ListRecordReviewDecisions(ctx, set.ID)
	if err != nil {
		return nil, err
	}
	view := &ApprovedRecordSetView{ID: set.ID, ValidationResultSetID: set.ValidationResultSetID,
		TargetDatasetID: set.TargetDatasetID, TargetSchemaID: set.TargetSchemaID,
		SourceStepRunID: set.SourceStepRunID, Reviewer: set.Reviewer, Rationale: set.Rationale,
		ReviewHash: set.ReviewHash, ReviewedThroughSeq: set.ReviewedThroughSeq,
		RecordCount: set.RecordCount, ApprovedCount: set.ApprovedCount,
		EditedCount: set.EditedCount, ExcludedCount: set.ExcludedCount,
		CreatedAt: set.CreatedAt, Decisions: make([]RecordReviewDecisionView, len(decisions))}
	for i, decision := range decisions {
		view.Decisions[i] = RecordReviewDecisionView{ID: decision.ID,
			ValidationResultID:  decision.ValidationResultID,
			TransformedRecordID: decision.TransformedRecordID, Ordinal: decision.Ordinal,
			Action: decision.Action, Fields: decision.Fields, ItemKey: decision.ItemKey,
			Fingerprint: decision.Fingerprint, Issues: decision.Issues,
			Provenance: decision.Provenance, Note: decision.Note, CreatedAt: decision.CreatedAt}
	}
	return view, nil
}
