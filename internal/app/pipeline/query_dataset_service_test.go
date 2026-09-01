package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

func TestQueryDatasetServiceProcessesOnlyNewIncrementAndPreservesLineage(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryPipelineRepo()
	datasets := NewDatasetService(repo)
	queries, err := NewQueryDatasetService(repo, datasets)
	if err != nil {
		t.Fatal(err)
	}
	source, target := createQueryDerivationDatasets(t, ctx, datasets)

	commitSourceItems(t, ctx, datasets, source.ID,
		BatchItemInput{Fields: map[string]any{"sku": "A-1", "name": "过温保护", "description": "温度超过阈值后关断",
			"aliases": []any{"OTP", "高温保护"}, "keywords": "温度，关断", "product_family": "X100", "module": "power"},
			Provenance: model.ItemProvenance{SourceRefs: []model.SourceReference{{AssetID: "asset-1", BlockID: "block-1", PageNo: 18, Quote: "过温保护"}}}},
		BatchItemInput{Fields: map[string]any{"sku": "A-2", "name": "欠压锁定", "description": "电压低于阈值时禁止启动",
			"aliases": []any{"UVLO"}, "keywords": "电压;启动", "product_family": "X100", "module": "power"}},
	)
	config := queryDerivationTestConfig()
	first, err := queries.Derive(ctx, DeriveQueryDatasetInput{SourceDatasetID: source.ID,
		SourceThroughSeq: 2, TargetDatasetID: target.ID, Config: config,
		ProducerWorkflowRunID: "task-1", ProducerNodeRunID: "step-1", ProducerAttempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if first.SourceItemCount != 2 || first.QueryItemCount != 2 || first.Cursor.ProcessedThroughSeq != 2 ||
		first.Batch.FromSeq != 1 || first.Batch.ToSeq != 2 {
		t.Fatalf("首批派生位点错误: %+v", first)
	}
	firstItems := repo.items[target.ID]
	if len(firstItems) != 2 {
		t.Fatalf("首批 Query Item 数量错误: %+v", firstItems)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(firstItems[0].Fields), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["source_item_id"] == "" || fields["source_fingerprint"] == "" || fields["title"] == "" {
		t.Fatalf("Query Item 缺少稳定来源身份: %+v", fields)
	}
	foundOriginalBlock := false
	for _, item := range firstItems {
		var provenance model.ItemProvenance
		if err := json.Unmarshal([]byte(item.Provenance), &provenance); err != nil {
			t.Fatal(err)
		}
		if provenance.PipelineKey != config.PipelineKey || provenance.SourceDatasetItemID == "" ||
			provenance.SourceFingerprint == "" || len(provenance.SourceRefs) == 0 {
			t.Fatalf("Query Item provenance 不完整: %+v", provenance)
		}
		for _, ref := range provenance.SourceRefs {
			if ref.AssetID == "asset-1" && ref.BlockID == "block-1" && ref.DatasetItemID != "" {
				foundOriginalBlock = true
			}
		}
	}
	if !foundOriginalBlock {
		t.Fatal("Query Item 未保留 Base DatasetItem → Asset/Block 的完整来源链")
	}
	retried, err := queries.Derive(ctx, DeriveQueryDatasetInput{SourceDatasetID: source.ID,
		SourceThroughSeq: 2, TargetDatasetID: target.ID, Config: config,
		ProducerWorkflowRunID: "task-1", ProducerNodeRunID: "step-1", ProducerAttempt: 2})
	if err != nil || retried.Batch.ID != first.Batch.ID || len(repo.items[target.ID]) != 2 {
		t.Fatalf("提交后的同 Task attempt 必须复用原 Batch: result=%+v err=%v", retried, err)
	}
	batchCount := len(repo.batches)
	_, err = queries.Derive(ctx, DeriveQueryDatasetInput{SourceDatasetID: source.ID,
		SourceThroughSeq: 2, TargetDatasetID: target.ID, Config: config,
		ProducerWorkflowRunID: "task-noop", ProducerNodeRunID: "step-noop", ProducerAttempt: 1})
	if !errors.Is(err, ErrNoQueryDatasetIncrement) || len(repo.batches) != batchCount {
		t.Fatalf("无增量 Task 应拒绝且不遗留 staging Batch: err=%v batches=%d", err, len(repo.batches))
	}

	commitSourceItems(t, ctx, datasets, source.ID, BatchItemInput{Fields: map[string]any{
		"sku": "A-3", "name": "短路保护", "description": "检测短路后立即关断",
		"aliases": []any{"SCP"}, "keywords": "短路|关断", "product_family": "X200", "module": "power",
	}})
	second, err := queries.Derive(ctx, DeriveQueryDatasetInput{SourceDatasetID: source.ID,
		SourceThroughSeq: 3, TargetDatasetID: target.ID, Config: config,
		ProducerWorkflowRunID: "task-2", ProducerNodeRunID: "step-2", ProducerAttempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if second.SourceItemCount != 1 || second.QueryItemCount != 1 || second.Cursor.ProcessedThroughSeq != 3 ||
		second.Batch.FromSeq != 3 || second.Batch.ToSeq != 3 || len(repo.items[target.ID]) != 3 {
		t.Fatalf("第二批必须只消费新增 Item: result=%+v target=%+v", second, repo.items[target.ID])
	}
}

func TestQueryDatasetServiceDoesNotAdvanceCursorWhenTargetCommitFails(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryPipelineRepo()
	datasets := NewDatasetService(repo)
	queries, _ := NewQueryDatasetService(repo, datasets)
	source, target := createQueryDerivationDatasets(t, ctx, datasets)
	commitSourceItems(t, ctx, datasets, source.ID, BatchItemInput{Fields: map[string]any{
		"sku": "F-1", "name": "故障保护", "description": "用于验证事务失败",
		"aliases": []any{}, "keywords": "故障", "product_family": "X", "module": "test",
	}})
	repo.failQueryCommit = true
	input := DeriveQueryDatasetInput{SourceDatasetID: source.ID, SourceThroughSeq: 1,
		TargetDatasetID: target.ID, Config: queryDerivationTestConfig(), ProducerWorkflowRunID: "task-fail",
		ProducerNodeRunID: "step-fail", ProducerAttempt: 1}
	if _, err := queries.Derive(ctx, input); err == nil {
		t.Fatal("目标 Batch 失败应返回错误")
	}
	cursor, err := repo.GetPipelineCursor(ctx, input.Config.PipelineKey, source.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.ProcessedThroughSeq != 0 || repo.datasets[target.ID].CurrentSeq != 0 || len(repo.items[target.ID]) != 0 {
		t.Fatalf("失败时 Batch 与 Cursor 都不能提交: cursor=%+v target=%+v items=%+v",
			cursor, repo.datasets[target.ID], repo.items[target.ID])
	}

	repo.failQueryCommit = false
	retried, err := queries.Derive(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Cursor.ProcessedThroughSeq != 1 || retried.Batch.ItemCount != 1 || len(repo.items[target.ID]) != 1 {
		t.Fatalf("同一 NodeRun 重试应复用 staging Batch 并成功推进: %+v", retried)
	}
}

func TestDeriveQueryItemsExpandsStableSemanticUnits(t *testing.T) {
	source := model.DatasetItem{ID: "item-source", Fingerprint: "fingerprint-source",
		Fields: `{"product_family":"X900","units":[
			{"unit_key":"otp","title":"过温保护","definition":"超过阈值关断","aliases":["OTP"],"keywords":"温度，关断"},
			{"unit_key":"uvlo","title":"欠压锁定","definition":"低于阈值禁用","aliases":["UVLO"],"keywords":"电压，启动"}
		]}`, Provenance: `{"source_refs":[{"asset_id":"asset","block_id":"block","page_no":7}]}`}
	config := QueryDerivationConfig{PipelineKey: "semantic_units_v1", SemanticUnitsField: "units",
		UnitKeyField: "unit_key", TitleField: "title", DefinitionFields: []string{"definition"},
		AliasFields: []string{"aliases"}, KeywordFields: []string{"keywords"},
		FacetFields: map[string]string{"product_family": "product_family"}}
	items, err := deriveQueryItems(source, "base-dataset", config)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Fields["semantic_unit_key"] != "item-source:otp" ||
		items[1].Fields["semantic_unit_key"] != "item-source:uvlo" ||
		items[0].Fields["title"] != "过温保护" {
		t.Fatalf("语义单元未按稳定 unit_key 展开: %+v", items)
	}
	facets := items[0].Fields["facets"].(map[string]any)
	if facets["product_family"] != "X900" {
		t.Fatalf("语义单元应回退继承 Base Item facet: %+v", facets)
	}
	var provenance model.ItemProvenance
	raw, _ := json.Marshal(items[0].Provenance)
	if err := json.Unmarshal(raw, &provenance); err != nil || len(provenance.SourceRefs) != 2 ||
		provenance.SourceRefs[1].DatasetItemID != source.ID || provenance.SourceRefs[1].BlockID != "block" {
		t.Fatalf("展开后的每个语义单元必须保留完整 lineage: %+v err=%v", provenance, err)
	}
}

func createQueryDerivationDatasets(t *testing.T, ctx context.Context,
	service *DatasetService) (*model.Dataset, *model.Dataset) {
	t.Helper()
	baseSchema, err := service.CreateSchema(ctx, CreateSchemaInput{Name: "Base Spec", JSONSchema: json.RawMessage(`{
		"type":"object","properties":{
			"sku":{"type":"string"},"name":{"type":"string"},"description":{"type":"string"},
			"aliases":{"type":"array","items":{"type":"string"}},"keywords":{"type":"string"},
			"product_family":{"type":"string"},"module":{"type":"string"}
		},"required":["sku","name","description","aliases","keywords","product_family","module"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	base, err := service.CreateDataset(ctx, CreateDatasetInput{Name: "Base", Purpose: model.DatasetPurposeBase,
		SchemaID: baseSchema.ID, KeyFields: []string{"sku"}})
	if err != nil {
		t.Fatal(err)
	}
	querySchema, err := service.CreateSchema(ctx, CreateSchemaInput{Name: "Query Contract", JSONSchema: queryDatasetTestSchema()})
	if err != nil {
		t.Fatal(err)
	}
	query, err := service.CreateDataset(ctx, CreateDatasetInput{Name: "Query", Purpose: model.DatasetPurposeQuery,
		SchemaID: querySchema.ID, KeyFields: []string{"semantic_unit_key"}})
	if err != nil {
		t.Fatal(err)
	}
	return base, query
}

func commitSourceItems(t *testing.T, ctx context.Context, service *DatasetService,
	datasetID string, items ...BatchItemInput) {
	t.Helper()
	batch, err := service.CreateBatch(ctx, CreateBatchInput{DatasetID: datasetID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CommitBatch(ctx, batch.ID, items); err != nil {
		t.Fatal(err)
	}
}

func queryDerivationTestConfig() QueryDerivationConfig {
	return QueryDerivationConfig{PipelineKey: "spec_query_v1", TitleField: "name",
		DefinitionFields: []string{"description"}, AliasFields: []string{"aliases"},
		KeywordFields: []string{"keywords"}, FacetFields: map[string]string{
			"product_family": "product_family", "module": "module",
		}}
}

func queryDatasetTestSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object","properties":{
			"semantic_unit_key":{"type":"string"},"source_item_id":{"type":"string"},
			"source_fingerprint":{"type":"string"},"title":{"type":"string"},
			"aliases":{"type":"array","items":{"type":"string"}},"definition":{"type":"string"},
			"keywords":{"type":"array","items":{"type":"string"}},
			"facets":{"type":"object","properties":{"product_family":{"type":"string"},"module":{"type":"string"}},"additionalProperties":false},
			"source_refs":{"type":"array","items":{"type":"object","properties":{
				"dataset_item_id":{"type":"string"},"asset_id":{"type":"string"},"block_id":{"type":"string"},
				"page_no":{"type":"integer"},"quote":{"type":"string"}
			},"required":["dataset_item_id"],"additionalProperties":false}}
		},"required":["semantic_unit_key","source_item_id","source_fingerprint","title","aliases","definition","keywords","facets","source_refs"],
		"additionalProperties":false}`)
}

func mustJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func cursorKey(pipelineKey, sourceDatasetID, targetDatasetID string) string {
	return fmt.Sprintf("%s:%s:%s", pipelineKey, sourceDatasetID, targetDatasetID)
}

func (r *memoryPipelineRepo) GetPipelineCursor(_ context.Context, pipelineKey, sourceDatasetID,
	targetDatasetID string) (*model.PipelineCursor, error) {
	cursor, ok := r.cursors[cursorKey(pipelineKey, sourceDatasetID, targetDatasetID)]
	if !ok {
		return nil, errors.New("not found")
	}
	clone := *cursor
	return &clone, nil
}

func (r *memoryPipelineRepo) GetOrCreatePipelineCursor(_ context.Context, pipelineKey, sourceDatasetID,
	targetDatasetID string) (*model.PipelineCursor, error) {
	key := cursorKey(pipelineKey, sourceDatasetID, targetDatasetID)
	if _, ok := r.cursors[key]; !ok {
		r.cursors[key] = &model.PipelineCursor{ID: r.id("cursor"), PipelineKey: pipelineKey,
			SourceDatasetID: sourceDatasetID, TargetDatasetID: targetDatasetID}
	}
	clone := *r.cursors[key]
	return &clone, nil
}

func (r *memoryPipelineRepo) CommitQueryDatasetBatchForNode(ctx context.Context, batchID, _ string, _ int,
	payloadHash string, items []model.DatasetItem, cursorID string, expectedThroughSeq, advanceThroughSeq int64,
	lastSuccessTaskID string) (*model.DatasetBatch, *model.PipelineCursor, error) {
	if r.failQueryCommit {
		return nil, nil, errors.New("injected query batch failure")
	}
	var cursor *model.PipelineCursor
	for _, candidate := range r.cursors {
		if candidate.ID == cursorID {
			cursor = candidate
			break
		}
	}
	if cursor == nil || cursor.ProcessedThroughSeq != expectedThroughSeq {
		return nil, nil, port.ErrPipelineCursorConflict
	}
	batch, err := r.CommitDatasetBatch(ctx, batchID, payloadHash, items)
	if err != nil {
		return nil, nil, err
	}
	cursor.ProcessedThroughSeq = advanceThroughSeq
	cursor.LastSuccessRunID = lastSuccessTaskID
	clone := *cursor
	return batch, &clone, nil
}
