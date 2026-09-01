package pipeline

import (
	"context"
	"time"
)

type PipelineCursorView struct {
	ID                  string    `json:"id"`
	PipelineKey         string    `json:"pipeline_key"`
	SourceDatasetID     string    `json:"source_dataset_id"`
	TargetDatasetID     string    `json:"target_dataset_id"`
	ProcessedThroughSeq int64     `json:"processed_through_seq"`
	LastSuccessRunID    string    `json:"last_success_run_id,omitempty"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func (s *QueryDatasetService) GetCursorView(ctx context.Context, pipelineKey, sourceDatasetID,
	targetDatasetID string) (*PipelineCursorView, error) {
	cursor, err := s.GetCursor(ctx, pipelineKey, sourceDatasetID, targetDatasetID)
	if err != nil {
		return nil, err
	}
	return &PipelineCursorView{ID: cursor.ID, PipelineKey: cursor.PipelineKey,
		SourceDatasetID: cursor.SourceDatasetID, TargetDatasetID: cursor.TargetDatasetID,
		ProcessedThroughSeq: cursor.ProcessedThroughSeq, LastSuccessRunID: cursor.LastSuccessRunID,
		UpdatedAt: cursor.UpdatedAt}, nil
}
