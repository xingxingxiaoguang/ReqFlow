package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type DatasetService struct {
	repo port.DatasetPipelineRepo
}

func NewDatasetService(repo port.DatasetPipelineRepo) *DatasetService {
	return &DatasetService{repo: repo}
}

type CreateSchemaInput struct {
	WorkspaceID string
	Name        string
	Description string
	JSONSchema  json.RawMessage
	UISchema    json.RawMessage
}

func (s *DatasetService) CreateSchema(ctx context.Context, in CreateSchemaInput) (*model.DatasetSchemaDefinition, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("Schema 名称不能为空")
	}
	definition, hash, err := logic.NormalizeDatasetSchema(in.JSONSchema)
	if err != nil {
		return nil, err
	}
	ui, err := logic.NormalizeUISchema(in.UISchema)
	if err != nil {
		return nil, err
	}
	schema := &model.DatasetSchemaDefinition{
		WorkspaceID: strings.TrimSpace(in.WorkspaceID),
		Name:        name, Description: strings.TrimSpace(in.Description),
		JSONSchema: definition, UISchema: ui, SchemaHash: hash,
	}
	if schema.WorkspaceID == "" {
		schema.WorkspaceID = "default"
	}
	if err := s.repo.CreateDatasetSchema(ctx, schema); err != nil {
		return nil, err
	}
	return schema, nil
}

type CreateDatasetInput struct {
	WorkspaceID string
	Name        string
	Description string
	Purpose     model.DatasetPurpose
	SchemaID    string
	KeyFields   []string
}

func (s *DatasetService) CreateDataset(ctx context.Context, in CreateDatasetInput) (*model.Dataset, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("Dataset 名称不能为空")
	}
	if !validDatasetPurpose(in.Purpose) {
		return nil, fmt.Errorf("Dataset purpose 非法: %s", in.Purpose)
	}
	schema, err := s.repo.GetDatasetSchema(ctx, in.SchemaID)
	if err != nil {
		return nil, fmt.Errorf("读取 Dataset Schema: %w", err)
	}
	if err := logic.ValidateDatasetKeyFields(schema.JSONSchema, in.KeyFields); err != nil {
		return nil, err
	}
	dataset := &model.Dataset{
		WorkspaceID: strings.TrimSpace(in.WorkspaceID),
		Name:        name, Description: strings.TrimSpace(in.Description),
		Purpose: in.Purpose, Type: string(in.Purpose),
		SchemaID: schema.ID, Schema: string(schema.JSONSchema),
		KeyFields: append([]string(nil), in.KeyFields...),
		Status:    model.DatasetStatusActive,
	}
	if dataset.WorkspaceID == "" {
		dataset.WorkspaceID = "default"
	}
	if err := s.repo.CreateAppendDataset(ctx, dataset); err != nil {
		return nil, err
	}
	return dataset, nil
}

type CreateBatchInput struct {
	DatasetID       string
	SourceTaskID    string
	SourceStepRunID string
}

func (s *DatasetService) CreateBatch(ctx context.Context, in CreateBatchInput) (*model.DatasetBatch, error) {
	if strings.TrimSpace(in.DatasetID) == "" {
		return nil, fmt.Errorf("dataset_id 不能为空")
	}
	if _, err := s.repo.GetAppendDataset(ctx, in.DatasetID); err != nil {
		return nil, fmt.Errorf("读取 Dataset: %w", err)
	}
	batch := &model.DatasetBatch{
		DatasetID:       in.DatasetID,
		SourceTaskID:    strings.TrimSpace(in.SourceTaskID),
		SourceStepRunID: strings.TrimSpace(in.SourceStepRunID),
		Status:          model.DatasetBatchStaging,
	}
	if err := s.repo.CreateDatasetBatch(ctx, batch); err != nil {
		return nil, err
	}
	return batch, nil
}

// GetOrCreateBatch 以 source_step_run_id 为幂等键供 data.publish 使用。人工 HTTP
// 创建 Batch 仍走 CreateBatch，不会意外复用另一个业务动作。
func (s *DatasetService) GetOrCreateBatch(ctx context.Context, in CreateBatchInput,
	producerAttempt int) (*model.DatasetBatch, error) {
	if strings.TrimSpace(in.DatasetID) == "" || strings.TrimSpace(in.SourceStepRunID) == "" {
		return nil, fmt.Errorf("dataset_id 和 source_step_run_id 不能为空")
	}
	if _, err := s.repo.GetAppendDataset(ctx, in.DatasetID); err != nil {
		return nil, fmt.Errorf("读取 Dataset: %w", err)
	}
	return s.repo.GetOrCreateDatasetBatchForStep(ctx, &model.DatasetBatch{DatasetID: in.DatasetID,
		SourceTaskID: strings.TrimSpace(in.SourceTaskID), SourceStepRunID: strings.TrimSpace(in.SourceStepRunID),
		Status: model.DatasetBatchStaging}, producerAttempt)
}

type BatchItemInput struct {
	Fields     map[string]any
	Provenance model.ItemProvenance
}

func (s *DatasetService) CommitBatch(ctx context.Context, batchID string, inputs []BatchItemInput) (*model.DatasetBatch, error) {
	batch, items, err := s.prepareBatchItems(ctx, batchID, inputs)
	if err != nil {
		return nil, err
	}
	payloadHash := datasetBatchPayloadHash(items)
	return s.repo.CommitDatasetBatch(ctx, batch.ID, payloadHash, items)
}

func (s *DatasetService) CommitBatchForStep(ctx context.Context, batchID, sourceStepRunID string,
	producerAttempt int, inputs []BatchItemInput) (*model.DatasetBatch, error) {
	batch, items, err := s.prepareBatchItems(ctx, batchID, inputs)
	if err != nil {
		return nil, err
	}
	payloadHash := datasetBatchPayloadHash(items)
	return s.repo.CommitDatasetBatchForStep(ctx, batch.ID, sourceStepRunID, producerAttempt, payloadHash, items)
}

func (s *DatasetService) prepareBatchItems(ctx context.Context, batchID string,
	inputs []BatchItemInput) (*model.DatasetBatch, []model.DatasetItem, error) {
	batch, err := s.repo.GetDatasetBatch(ctx, batchID)
	if err != nil {
		return nil, nil, fmt.Errorf("读取 Batch: %w", err)
	}
	dataset, err := s.repo.GetAppendDataset(ctx, batch.DatasetID)
	if err != nil {
		return nil, nil, fmt.Errorf("读取 Dataset: %w", err)
	}
	if dataset.Status != model.DatasetStatusActive {
		return nil, nil, fmt.Errorf("Dataset %s 当前状态 %s 不允许追加", dataset.ID, dataset.Status)
	}
	schema, err := s.repo.GetDatasetSchema(ctx, dataset.SchemaID)
	if err != nil {
		return nil, nil, fmt.Errorf("读取 Dataset Schema: %w", err)
	}
	if len(inputs) == 0 {
		return nil, nil, fmt.Errorf("不能提交空 Batch")
	}

	items := make([]model.DatasetItem, 0, len(inputs))
	seen := make(map[string]bool, len(inputs))
	for i, input := range inputs {
		raw, err := json.Marshal(input.Fields)
		if err != nil {
			return nil, nil, fmt.Errorf("第 %d 条字段值序列化失败: %w", i+1, err)
		}
		fields, err := logic.NormalizeDatasetItem(schema.JSONSchema, raw)
		if err != nil {
			return nil, nil, fmt.Errorf("第 %d 条数据不符合 Schema: %w", i+1, err)
		}
		itemKey, fingerprint, err := logic.DatasetItemIdentity(schema.SchemaHash, dataset.KeyFields, fields)
		if err != nil {
			return nil, nil, fmt.Errorf("第 %d 条数据主键非法: %w", i+1, err)
		}
		if seen[itemKey] {
			return nil, nil, fmt.Errorf("Batch 内存在重复业务主键: %s", itemKey)
		}
		seen[itemKey] = true
		provenance, err := json.Marshal(input.Provenance)
		if err != nil {
			return nil, nil, fmt.Errorf("第 %d 条 provenance 序列化失败: %w", i+1, err)
		}
		items = append(items, model.DatasetItem{
			DatasetID: dataset.ID, BatchID: batch.ID,
			Fields: string(fields), ItemKey: itemKey, Fingerprint: fingerprint,
			Provenance: string(provenance), SourceTaskID: batch.SourceTaskID,
		})
	}

	// 固定排序让重试时的 seq 分配和 payload_hash 不受输入顺序影响。
	sort.Slice(items, func(i, j int) bool { return items[i].ItemKey < items[j].ItemKey })
	return batch, items, nil
}

func datasetBatchPayloadHash(items []model.DatasetItem) string {
	h := sha256.New()
	for _, item := range items {
		_, _ = h.Write([]byte(item.ItemKey))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(item.Fingerprint))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(item.Provenance))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func validDatasetPurpose(purpose model.DatasetPurpose) bool {
	switch purpose {
	case model.DatasetPurposeBase, model.DatasetPurposeQuery, model.DatasetPurposeAnalysis,
		model.DatasetPurposeGraphNode, model.DatasetPurposeGraphEdge:
		return true
	}
	return false
}
