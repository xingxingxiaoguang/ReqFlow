package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type Service struct{ repo port.V2CatalogRepo }

func NewService(repo port.V2CatalogRepo) (*Service, error) {
	if repo == nil {
		return nil, fmt.Errorf("V2 catalog repo is required")
	}
	return &Service{repo: repo}, nil
}

type Query struct {
	WorkspaceID    string
	Status         string
	Purpose        string
	TargetSchemaID string
	Limit          int
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

type DatasetView struct {
	ID          string               `json:"id"`
	WorkspaceID string               `json:"workspace_id"`
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	Purpose     model.DatasetPurpose `json:"purpose"`
	SchemaID    string               `json:"schema_id"`
	KeyFields   []string             `json:"key_fields"`
	Status      string               `json:"status"`
	CurrentSeq  int64                `json:"current_seq"`
	ItemCount   int                  `json:"item_count"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

type BatchView struct {
	ID              string    `json:"id"`
	DatasetID       string    `json:"dataset_id"`
	SourceTaskID    string    `json:"source_task_id,omitempty"`
	SourceStepRunID string    `json:"source_step_run_id,omitempty"`
	Status          string    `json:"status"`
	ItemCount       int       `json:"item_count"`
	FromSeq         int64     `json:"from_seq"`
	ToSeq           int64     `json:"to_seq"`
	PayloadHash     string    `json:"payload_hash,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	CommittedAt     time.Time `json:"committed_at,omitempty"`
}

type AssetSetView struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	CreatedBy   string    `json:"created_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type ExtractionProfileView struct {
	ID                string    `json:"id"`
	WorkspaceID       string    `json:"workspace_id"`
	Name              string    `json:"name"`
	TargetSchemaID    string    `json:"target_schema_id"`
	RecordGranularity string    `json:"record_granularity"`
	ProfileHash       string    `json:"profile_hash"`
	CreatedAt         time.Time `json:"created_at"`
}

type ArchivedTaskView struct {
	ID           string    `json:"id"`
	DefinitionID string    `json:"definition_id"`
	Type         string    `json:"type"`
	Title        string    `json:"title"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (s *Service) ListDefinitions(ctx context.Context, query Query) ([]model.TaskDefinition, error) {
	query = normalize(query)
	return s.repo.ListTaskDefinitions(ctx, query.WorkspaceID, query.Status, query.Limit)
}

func (s *Service) ListSchemas(ctx context.Context, query Query) ([]SchemaView, error) {
	query = normalize(query)
	items, err := s.repo.ListDatasetSchemas(ctx, query.WorkspaceID, query.Limit)
	if err != nil {
		return nil, err
	}
	views := make([]SchemaView, len(items))
	for i, item := range items {
		views[i] = SchemaView{ID: item.ID, WorkspaceID: item.WorkspaceID, Name: item.Name,
			Description: item.Description, JSONSchema: item.JSONSchema, UISchema: item.UISchema,
			SchemaHash: item.SchemaHash, CreatedAt: item.CreatedAt}
	}
	return views, nil
}

func (s *Service) ListDatasets(ctx context.Context, query Query) ([]DatasetView, error) {
	query = normalize(query)
	items, err := s.repo.ListAppendDatasets(ctx, query.WorkspaceID, query.Status,
		model.DatasetPurpose(strings.TrimSpace(query.Purpose)), query.Limit)
	if err != nil {
		return nil, err
	}
	views := make([]DatasetView, len(items))
	for i, item := range items {
		views[i] = DatasetView{ID: item.ID, WorkspaceID: item.WorkspaceID, Name: item.Name,
			Description: item.Description, Purpose: item.Purpose, SchemaID: item.SchemaID,
			KeyFields: item.KeyFields, Status: item.Status, CurrentSeq: item.CurrentSeq,
			ItemCount: item.ItemCount, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
	}
	return views, nil
}

func (s *Service) ListBatches(ctx context.Context, datasetID string, limit int) ([]BatchView, error) {
	items, err := s.repo.ListDatasetBatches(ctx, strings.TrimSpace(datasetID), normalizeLimit(limit))
	if err != nil {
		return nil, err
	}
	views := make([]BatchView, len(items))
	for i, item := range items {
		views[i] = BatchView{ID: item.ID, DatasetID: item.DatasetID, SourceTaskID: item.SourceTaskID,
			SourceStepRunID: item.SourceStepRunID, Status: item.Status, ItemCount: item.ItemCount,
			FromSeq: item.FromSeq, ToSeq: item.ToSeq, PayloadHash: item.PayloadHash,
			CreatedAt: item.CreatedAt, CommittedAt: item.CommittedAt}
	}
	return views, nil
}

func (s *Service) ArchiveDataset(ctx context.Context, id string) error {
	return s.repo.SetAppendDatasetStatus(ctx, strings.TrimSpace(id), model.DatasetStatusActive, model.DatasetStatusArchived)
}

func (s *Service) RestoreDataset(ctx context.Context, id string) error {
	return s.repo.SetAppendDatasetStatus(ctx, strings.TrimSpace(id), model.DatasetStatusArchived, model.DatasetStatusActive)
}

func (s *Service) ListAssetSets(ctx context.Context, query Query) ([]AssetSetView, error) {
	query = normalize(query)
	items, err := s.repo.ListAssetSets(ctx, query.WorkspaceID, query.Limit)
	if err != nil {
		return nil, err
	}
	views := make([]AssetSetView, len(items))
	for i, item := range items {
		views[i] = AssetSetView{ID: item.ID, WorkspaceID: item.WorkspaceID,
			Name: item.Name, CreatedBy: item.CreatedBy, CreatedAt: item.CreatedAt}
	}
	return views, nil
}

func (s *Service) ListExtractionProfiles(ctx context.Context, query Query) ([]ExtractionProfileView, error) {
	query = normalize(query)
	items, err := s.repo.ListExtractionProfiles(ctx, query.WorkspaceID, query.TargetSchemaID, query.Limit)
	if err != nil {
		return nil, err
	}
	views := make([]ExtractionProfileView, len(items))
	for i, item := range items {
		views[i] = ExtractionProfileView{ID: item.ID, WorkspaceID: item.WorkspaceID,
			Name: item.Name, TargetSchemaID: item.TargetSchemaID,
			RecordGranularity: item.RecordGranularity, ProfileHash: item.ProfileHash,
			CreatedAt: item.CreatedAt}
	}
	return views, nil
}

func (s *Service) ArchiveTask(ctx context.Context, id string) error {
	return s.repo.ArchiveOrchestratorTask(ctx, strings.TrimSpace(id))
}

func (s *Service) RestoreTask(ctx context.Context, id string) error {
	return s.repo.RestoreOrchestratorTask(ctx, strings.TrimSpace(id))
}

func (s *Service) ListArchivedTasks(ctx context.Context, query Query) ([]ArchivedTaskView, error) {
	query = normalize(query)
	items, err := s.repo.ListArchivedOrchestratorTasks(ctx, query.WorkspaceID, query.Limit)
	if err != nil {
		return nil, err
	}
	views := make([]ArchivedTaskView, len(items))
	for i, item := range items {
		views[i] = ArchivedTaskView{ID: item.ID, DefinitionID: item.DefinitionID,
			Type: item.Type, Title: item.Title, Status: item.Status,
			CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
	}
	return views, nil
}

func normalize(query Query) Query {
	query.WorkspaceID = strings.TrimSpace(query.WorkspaceID)
	if query.WorkspaceID == "" {
		query.WorkspaceID = "default"
	}
	query.Status = strings.TrimSpace(query.Status)
	query.TargetSchemaID = strings.TrimSpace(query.TargetSchemaID)
	query.Limit = normalizeLimit(query.Limit)
	return query
}

func normalizeLimit(limit int) int {
	if limit < 1 || limit > 200 {
		return 100
	}
	return limit
}
