package pipeline

import (
	"context"
	"encoding/json"
	"time"

	"reqflow/internal/domain/model"
)

type CreateSchemaRequest struct {
	WorkspaceID string          `json:"workspace_id,omitempty"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	JSONSchema  json.RawMessage `json:"json_schema"`
	UISchema    json.RawMessage `json:"ui_schema,omitempty"`
}

type SchemaView struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspace_id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	JSONSchema  json.RawMessage `json:"json_schema"`
	UISchema    json.RawMessage `json:"ui_schema"`
	SchemaHash  string          `json:"schema_hash"`
	CreatedAt   time.Time       `json:"created_at"`
}

func (s *DatasetService) RegisterSchema(ctx context.Context, request CreateSchemaRequest) (*SchemaView, error) {
	schema, err := s.CreateSchema(ctx, CreateSchemaInput(request))
	if err != nil {
		return nil, err
	}
	view := schemaView(schema)
	return &view, nil
}

func (s *DatasetService) GetSchemaView(ctx context.Context, id string) (*SchemaView, error) {
	schema, err := s.repo.GetDatasetSchema(ctx, id)
	if err != nil {
		return nil, err
	}
	view := schemaView(schema)
	return &view, nil
}

type CreateDatasetRequest struct {
	WorkspaceID string   `json:"workspace_id,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Purpose     string   `json:"purpose"`
	SchemaID    string   `json:"schema_id"`
	KeyFields   []string `json:"key_fields"`
}

type DatasetView struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Purpose     string    `json:"purpose"`
	SchemaID    string    `json:"schema_id"`
	KeyFields   []string  `json:"key_fields"`
	Status      string    `json:"status"`
	CurrentSeq  int64     `json:"current_seq"`
	ItemCount   int       `json:"item_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (s *DatasetService) RegisterDataset(ctx context.Context, request CreateDatasetRequest) (*DatasetView, error) {
	dataset, err := s.CreateDataset(ctx, CreateDatasetInput{WorkspaceID: request.WorkspaceID,
		Name: request.Name, Description: request.Description, Purpose: model.DatasetPurpose(request.Purpose),
		SchemaID: request.SchemaID, KeyFields: request.KeyFields})
	if err != nil {
		return nil, err
	}
	view := datasetView(dataset)
	return &view, nil
}

func (s *DatasetService) GetDatasetView(ctx context.Context, id string) (*DatasetView, error) {
	dataset, err := s.repo.GetAppendDataset(ctx, id)
	if err != nil {
		return nil, err
	}
	view := datasetView(dataset)
	return &view, nil
}

type CreateBatchRequest struct {
	ProducerWorkflowRunID string `json:"producer_workflow_run_id,omitempty"`
	ProducerNodeRunID     string `json:"producer_node_run_id,omitempty"`
}

type BatchView struct {
	ID                    string    `json:"id"`
	DatasetID             string    `json:"dataset_id"`
	ProducerWorkflowRunID string    `json:"producer_workflow_run_id,omitempty"`
	ProducerNodeRunID     string    `json:"producer_node_run_id,omitempty"`
	Status                string    `json:"status"`
	ItemCount             int       `json:"item_count"`
	FromSeq               int64     `json:"from_seq"`
	ToSeq                 int64     `json:"to_seq"`
	PayloadHash           string    `json:"payload_hash,omitempty"`
	ErrorMessage          string    `json:"error_message,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	CommittedAt           time.Time `json:"committed_at,omitempty"`
}

func (s *DatasetService) OpenBatch(ctx context.Context, datasetID string, request CreateBatchRequest) (*BatchView, error) {
	batch, err := s.CreateBatch(ctx, CreateBatchInput{DatasetID: datasetID,
		ProducerWorkflowRunID: request.ProducerWorkflowRunID, ProducerNodeRunID: request.ProducerNodeRunID})
	if err != nil {
		return nil, err
	}
	view := batchView(batch)
	return &view, nil
}

type CommitBatchItem struct {
	Fields     map[string]any  `json:"fields"`
	Provenance ProvenanceInput `json:"provenance,omitempty"`
}

type SourceReferenceInput struct {
	DatasetItemID string `json:"dataset_item_id,omitempty"`
	AssetID       string `json:"asset_id,omitempty"`
	BlockID       string `json:"block_id,omitempty"`
	PageNo        int    `json:"page_no,omitempty"`
	Quote         string `json:"quote,omitempty"`
}

type ProvenanceInput struct {
	SourceRefs          []SourceReferenceInput `json:"source_refs,omitempty"`
	ExtractionProfileID string                 `json:"extraction_profile_id,omitempty"`
	Model               string                 `json:"model,omitempty"`
	PromptHash          string                 `json:"prompt_hash,omitempty"`
	QualityStatus       string                 `json:"quality_status,omitempty"`
}

type CommitBatchRequest struct {
	Items []CommitBatchItem `json:"items"`
}

func (s *DatasetService) PublishBatch(ctx context.Context, batchID string, request CommitBatchRequest) (*BatchView, error) {
	items := make([]BatchItemInput, len(request.Items))
	for i, item := range request.Items {
		references := make([]model.SourceReference, len(item.Provenance.SourceRefs))
		for j, reference := range item.Provenance.SourceRefs {
			references[j] = model.SourceReference{DatasetItemID: reference.DatasetItemID, AssetID: reference.AssetID,
				BlockID: reference.BlockID, PageNo: reference.PageNo, Quote: reference.Quote}
		}
		items[i] = BatchItemInput{Fields: item.Fields, Provenance: model.ItemProvenance{
			SourceRefs: references, ExtractionProfileID: item.Provenance.ExtractionProfileID,
			Model: item.Provenance.Model, PromptHash: item.Provenance.PromptHash,
			QualityStatus: item.Provenance.QualityStatus,
		}}
	}
	batch, err := s.CommitBatch(ctx, batchID, items)
	if err != nil {
		return nil, err
	}
	view := batchView(batch)
	return &view, nil
}

type DatasetItemView struct {
	ID          string          `json:"id"`
	DatasetID   string          `json:"dataset_id"`
	BatchID     string          `json:"batch_id"`
	Fields      json.RawMessage `json:"fields"`
	ItemKey     string          `json:"item_key"`
	Fingerprint string          `json:"fingerprint"`
	CommitSeq   int64           `json:"commit_seq"`
	Provenance  json.RawMessage `json:"provenance"`
	CreatedAt   time.Time       `json:"created_at"`
}

func (s *DatasetService) ListItems(ctx context.Context, datasetID string, afterSeq, throughSeq int64, limit int) ([]DatasetItemView, error) {
	if throughSeq <= 0 {
		dataset, err := s.repo.GetAppendDataset(ctx, datasetID)
		if err != nil {
			return nil, err
		}
		throughSeq = dataset.CurrentSeq
	}
	items, err := s.repo.ListDatasetItemsAfter(ctx, datasetID, afterSeq, throughSeq, limit)
	if err != nil {
		return nil, err
	}
	views := make([]DatasetItemView, len(items))
	for i, item := range items {
		views[i] = DatasetItemView{ID: item.ID, DatasetID: item.DatasetID, BatchID: item.BatchID,
			Fields: json.RawMessage(item.Fields), ItemKey: item.ItemKey, Fingerprint: item.Fingerprint,
			CommitSeq: item.CommitSeq, Provenance: json.RawMessage(item.Provenance), CreatedAt: item.CreatedAt}
	}
	return views, nil
}

func datasetView(dataset *model.Dataset) DatasetView {
	return DatasetView{ID: dataset.ID, WorkspaceID: dataset.WorkspaceID, Name: dataset.Name,
		Description: dataset.Description, Purpose: string(dataset.Purpose), SchemaID: dataset.SchemaID,
		KeyFields: dataset.KeyFields, Status: dataset.Status, CurrentSeq: dataset.CurrentSeq,
		ItemCount: dataset.ItemCount, CreatedAt: dataset.CreatedAt, UpdatedAt: dataset.UpdatedAt}
}

func schemaView(schema *model.DatasetSchemaDefinition) SchemaView {
	return SchemaView{ID: schema.ID, WorkspaceID: schema.WorkspaceID, Name: schema.Name,
		Description: schema.Description, JSONSchema: schema.JSONSchema, UISchema: schema.UISchema,
		SchemaHash: schema.SchemaHash, CreatedAt: schema.CreatedAt}
}

func batchView(batch *model.DatasetBatch) BatchView {
	return BatchView{ID: batch.ID, DatasetID: batch.DatasetID, ProducerWorkflowRunID: batch.ProducerWorkflowRunID,
		ProducerNodeRunID: batch.ProducerNodeRunID, Status: batch.Status, ItemCount: batch.ItemCount,
		FromSeq: batch.FromSeq, ToSeq: batch.ToSeq, PayloadHash: batch.PayloadHash,
		ErrorMessage: batch.ErrorMessage, CreatedAt: batch.CreatedAt, CommittedAt: batch.CommittedAt}
}
