package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

func TestDatasetServiceAppendsMultipleBatches(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryPipelineRepo()
	service := NewDatasetService(repo)

	schema, err := service.CreateSchema(ctx, CreateSchemaInput{
		Name: "产品规格",
		JSONSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"sku":{"type":"string","minLength":1},
				"name":{"type":"string"},
				"voltage":{"type":"number","minimum":0}
			},
			"required":["sku","name"]
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := service.CreateDataset(ctx, CreateDatasetInput{
		Name: "基础规格库", Purpose: model.DatasetPurposeBase,
		SchemaID: schema.ID, KeyFields: []string{"sku"},
	})
	if err != nil {
		t.Fatal(err)
	}

	batch1, err := service.CreateBatch(ctx, CreateBatchInput{DatasetID: dataset.ID})
	if err != nil {
		t.Fatal(err)
	}
	committed1, err := service.CommitBatch(ctx, batch1.ID, []BatchItemInput{
		{Fields: map[string]any{"sku": "X200", "name": "控制器", "voltage": 12}},
		{Fields: map[string]any{"sku": "X100", "name": "传感器", "voltage": 3.3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if committed1.FromSeq != 1 || committed1.ToSeq != 2 || committed1.ItemCount != 2 {
		t.Fatalf("Batch1 位点错误: %+v", committed1)
	}

	// 同一 Batch 同一载荷重试幂等，输入顺序变化也不影响 payload_hash。
	retried, err := service.CommitBatch(ctx, batch1.ID, []BatchItemInput{
		{Fields: map[string]any{"sku": "X100", "name": "传感器", "voltage": 3.3}},
		{Fields: map[string]any{"sku": "X200", "name": "控制器", "voltage": 12}},
	})
	if err != nil || retried.ToSeq != 2 {
		t.Fatalf("Batch 重试应幂等: %+v, %v", retried, err)
	}

	batch2, _ := service.CreateBatch(ctx, CreateBatchInput{DatasetID: dataset.ID})
	committed2, err := service.CommitBatch(ctx, batch2.ID, []BatchItemInput{
		{Fields: map[string]any{"sku": "X300", "name": "执行器", "voltage": 24}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if committed2.FromSeq != 3 || committed2.ToSeq != 3 {
		t.Fatalf("Batch2 应从上一位点继续: %+v", committed2)
	}
	items, err := repo.ListDatasetItemsAfter(ctx, dataset.ID, 1, 3, 10)
	if err != nil || len(items) != 2 || items[0].CommitSeq != 2 || items[1].CommitSeq != 3 {
		t.Fatalf("增量读取错误: %+v, %v", items, err)
	}
}

func TestDatasetServiceRejectsInvalidAndExistingKeys(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryPipelineRepo()
	service := NewDatasetService(repo)
	schema, _ := service.CreateSchema(ctx, CreateSchemaInput{
		Name: "规格", JSONSchema: json.RawMessage(`{"type":"object","properties":{"sku":{"type":"string"},"count":{"type":"integer","minimum":1}},"required":["sku"]}`),
	})
	dataset, _ := service.CreateDataset(ctx, CreateDatasetInput{
		Name: "规格库", Purpose: model.DatasetPurposeBase, SchemaID: schema.ID, KeyFields: []string{"sku"},
	})

	bad, _ := service.CreateBatch(ctx, CreateBatchInput{DatasetID: dataset.ID})
	if _, err := service.CommitBatch(ctx, bad.ID, []BatchItemInput{{Fields: map[string]any{"sku": "X", "count": 0}}}); err == nil {
		t.Fatal("不符合 Schema 的记录应拒绝")
	}

	first, _ := service.CreateBatch(ctx, CreateBatchInput{DatasetID: dataset.ID})
	if _, err := service.CommitBatch(ctx, first.ID, []BatchItemInput{{Fields: map[string]any{"sku": "X", "count": 1}}}); err != nil {
		t.Fatal(err)
	}
	duplicate, _ := service.CreateBatch(ctx, CreateBatchInput{DatasetID: dataset.ID})
	_, err := service.CommitBatch(ctx, duplicate.ID, []BatchItemInput{{Fields: map[string]any{"sku": "X", "count": 2}}})
	if !errors.Is(err, port.ErrDatasetItemKeyConflict) {
		t.Fatalf("已存在 key 应返回冲突，got %v", err)
	}
}

type memoryPipelineRepo struct {
	next            int
	schemas         map[string]*model.DatasetSchemaDefinition
	datasets        map[string]*model.Dataset
	batches         map[string]*model.DatasetBatch
	items           map[string][]model.DatasetItem
	cursors         map[string]*model.PipelineCursor
	failQueryCommit bool
}

func newMemoryPipelineRepo() *memoryPipelineRepo {
	return &memoryPipelineRepo{
		schemas: map[string]*model.DatasetSchemaDefinition{}, datasets: map[string]*model.Dataset{},
		batches: map[string]*model.DatasetBatch{}, items: map[string][]model.DatasetItem{},
		cursors: map[string]*model.PipelineCursor{},
	}
}

func (r *memoryPipelineRepo) id(prefix string) string {
	r.next++
	return fmt.Sprintf("%s-%d", prefix, r.next)
}

func (r *memoryPipelineRepo) CreateDatasetSchema(_ context.Context, schema *model.DatasetSchemaDefinition) error {
	schema.ID = r.id("schema")
	clone := *schema
	r.schemas[schema.ID] = &clone
	return nil
}

func (r *memoryPipelineRepo) GetDatasetSchema(_ context.Context, id string) (*model.DatasetSchemaDefinition, error) {
	value, ok := r.schemas[id]
	if !ok {
		return nil, errors.New("not found")
	}
	clone := *value
	return &clone, nil
}

func (r *memoryPipelineRepo) CreateAppendDataset(_ context.Context, dataset *model.Dataset) error {
	dataset.ID = r.id("dataset")
	clone := *dataset
	r.datasets[dataset.ID] = &clone
	return nil
}

func (r *memoryPipelineRepo) GetAppendDataset(_ context.Context, id string) (*model.Dataset, error) {
	value, ok := r.datasets[id]
	if !ok {
		return nil, errors.New("not found")
	}
	clone := *value
	return &clone, nil
}

func (r *memoryPipelineRepo) CreateDatasetBatch(_ context.Context, batch *model.DatasetBatch) error {
	batch.ID = r.id("batch")
	clone := *batch
	r.batches[batch.ID] = &clone
	return nil
}

func (r *memoryPipelineRepo) GetOrCreateDatasetBatchForStep(ctx context.Context, batch *model.DatasetBatch,
	_ int) (*model.DatasetBatch, error) {
	for _, stored := range r.batches {
		if stored.SourceStepRunID == batch.SourceStepRunID {
			clone := *stored
			return &clone, nil
		}
	}
	if err := r.CreateDatasetBatch(ctx, batch); err != nil {
		return nil, err
	}
	clone := *batch
	return &clone, nil
}

func (r *memoryPipelineRepo) GetDatasetBatch(_ context.Context, id string) (*model.DatasetBatch, error) {
	value, ok := r.batches[id]
	if !ok {
		return nil, errors.New("not found")
	}
	clone := *value
	return &clone, nil
}

func (r *memoryPipelineRepo) CommitDatasetBatch(_ context.Context, batchID, payloadHash string, items []model.DatasetItem) (*model.DatasetBatch, error) {
	batch := r.batches[batchID]
	if batch.Status == model.DatasetBatchCommitted {
		if batch.PayloadHash != payloadHash {
			return nil, port.ErrDatasetBatchNotWritable
		}
		clone := *batch
		return &clone, nil
	}
	dataset := r.datasets[batch.DatasetID]
	existing := map[string]bool{}
	for _, item := range r.items[dataset.ID] {
		existing[item.ItemKey] = true
	}
	for _, item := range items {
		if existing[item.ItemKey] {
			return nil, port.ErrDatasetItemKeyConflict
		}
	}
	from, to, err := logic.NextCommitRange(dataset.CurrentSeq, len(items))
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == "" {
			items[i].ID = r.id("item")
		}
		items[i].DatasetID = dataset.ID
		items[i].CommitSeq = from + int64(i)
		items[i].BatchID = batch.ID
		r.items[dataset.ID] = append(r.items[dataset.ID], items[i])
	}
	dataset.CurrentSeq = to
	dataset.ItemCount += len(items)
	batch.Status = model.DatasetBatchCommitted
	batch.FromSeq, batch.ToSeq = from, to
	batch.ItemCount, batch.PayloadHash = len(items), payloadHash
	clone := *batch
	return &clone, nil
}

func (r *memoryPipelineRepo) CommitDatasetBatchForStep(ctx context.Context, batchID, _ string, _ int,
	payloadHash string, items []model.DatasetItem) (*model.DatasetBatch, error) {
	return r.CommitDatasetBatch(ctx, batchID, payloadHash, items)
}

func (r *memoryPipelineRepo) ListDatasetItemsAfter(_ context.Context, datasetID string, afterSeq, throughSeq int64, limit int) ([]model.DatasetItem, error) {
	var out []model.DatasetItem
	for _, item := range r.items[datasetID] {
		if item.CommitSeq > afterSeq && item.CommitSeq <= throughSeq {
			out = append(out, item)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}
