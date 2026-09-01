package retrieval

import (
	"context"
	"encoding/json"
	"time"

	"reqflow/internal/domain/model"
)

type SnapshotView struct {
	ID                string          `json:"id"`
	DatasetID         string          `json:"dataset_id"`
	DataContractHash  string          `json:"data_contract_hash"`
	SearchSpec        json.RawMessage `json:"search_spec"`
	SearchSpecHash    string          `json:"search_spec_hash"`
	EmbeddingModel    string          `json:"embedding_model"`
	ProducerNodeRunID string          `json:"producer_node_run_id,omitempty"`
	SourceSeq         int64           `json:"source_seq"`
	Status            string          `json:"status"`
	LexicalRef        string          `json:"lexical_ref,omitempty"`
	VectorRef         string          `json:"vector_ref,omitempty"`
	LexicalCount      int             `json:"lexical_count"`
	VectorCount       int             `json:"vector_count"`
	FailureReason     string          `json:"failure_reason,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	ActivatedAt       time.Time       `json:"activated_at,omitempty"`
}

func (s *Service) GetSnapshotView(ctx context.Context, id string) (*SnapshotView, error) {
	snapshot, err := s.repo.GetRetrievalSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	view := snapshotView(snapshot)
	return &view, nil
}

func (s *Service) ListSnapshotViews(ctx context.Context, datasetID, searchSpecHash, status string,
	limit int) ([]SnapshotView, error) {
	snapshots, err := s.repo.ListRetrievalSnapshots(ctx, datasetID, searchSpecHash, status, limit)
	if err != nil {
		return nil, err
	}
	views := make([]SnapshotView, len(snapshots))
	for i := range snapshots {
		views[i] = snapshotView(&snapshots[i])
	}
	return views, nil
}

type SearchAPIRequest struct {
	RetrievalSnapshotID string                        `json:"retrieval_snapshot_id"`
	Query               string                        `json:"query"`
	Filters             map[string][]string           `json:"filters,omitempty"`
	Strategy            model.RetrievalSearchStrategy `json:"strategy"`
}

func (s *Service) SearchAPI(ctx context.Context, request SearchAPIRequest) (*SearchResponse, error) {
	return s.Search(ctx, SearchRequest(request))
}

func snapshotView(snapshot *model.RetrievalSnapshot) SnapshotView {
	return SnapshotView{ID: snapshot.ID, DatasetID: snapshot.DatasetID,
		DataContractHash: snapshot.DataContractHash, SearchSpec: snapshot.SearchSpec,
		SearchSpecHash: snapshot.SearchSpecHash, EmbeddingModel: snapshot.EmbeddingModel,
		ProducerNodeRunID: snapshot.ProducerNodeRunID,
		SourceSeq:         snapshot.SourceSeq, Status: snapshot.Status, LexicalRef: snapshot.LexicalRef,
		VectorRef: snapshot.VectorRef, LexicalCount: snapshot.LexicalCount,
		VectorCount: snapshot.VectorCount, FailureReason: snapshot.FailureReason,
		CreatedAt: snapshot.CreatedAt, ActivatedAt: snapshot.ActivatedAt}
}
