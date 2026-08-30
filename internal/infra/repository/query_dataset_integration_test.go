//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	apporchestrator "reqflow/internal/app/orchestrator"
	apppipeline "reqflow/internal/app/pipeline"
	"reqflow/internal/domain/model"
	"reqflow/internal/infra/database"
)

func TestIntegrationQueryDatasetIncrementUsesCursorWithoutReprocessing(t *testing.T) {
	ctx := context.Background()
	db, err := database.Connect(testDSN(), 3, 500)
	if err != nil {
		t.Skipf("本地 PG 不可用，跳过集成测试: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewPipelineRepo(db)
	datasets := apppipeline.NewDatasetService(repo)
	queries, _ := apppipeline.NewQueryDatasetService(repo, datasets)
	executor, _ := apppipeline.NewQueryDatasetDeriveExecutor(queries)
	registry, err := apporchestrator.NewRegistry(executor)
	if err != nil {
		t.Fatal(err)
	}
	definitions := apporchestrator.NewDefinitionService(repo, registry, repo)
	scheduler := apporchestrator.NewScheduler(repo)
	worker, err := apporchestrator.NewWorker(repo, registry, scheduler, apporchestrator.WorkerOptions{
		LeaseDuration: 2 * time.Second, PollInterval: 10 * time.Millisecond,
		RecoveryInterval: 100 * time.Millisecond, ReconcileLimit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, _ := apporchestrator.NewRuntimeService(repo, scheduler, worker)
	workerCtx, cancelWorker := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Run(workerCtx) }()
	t.Cleanup(func() {
		cancelWorker()
		select {
		case <-workerDone:
		case <-time.After(2 * time.Second):
		}
	})

	source, target, schemaIDs := createIntegrationQueryDatasets(t, ctx, datasets)
	definition := createIntegrationQueryDefinition(t, ctx, definitions)
	taskIDs := make([]string, 0, 2)
	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM datasets WHERE id IN (?, ?)`, source.ID, target.ID).Error
		for _, taskID := range taskIDs {
			_ = db.Exec(`DELETE FROM tasks WHERE id = ?`, taskID).Error
		}
		_ = db.Exec(`DELETE FROM task_definitions WHERE id = ?`, definition.ID).Error
		for _, schemaID := range schemaIDs {
			_ = db.Exec(`DELETE FROM dataset_schemas WHERE id = ?`, schemaID).Error
		}
	})

	commitIntegrationSourceItems(t, ctx, datasets, source.ID, "A-1", "过温保护", "温度超过阈值后关断",
		model.ItemProvenance{SourceRefs: []model.SourceReference{{AssetID: "11111111-1111-1111-1111-111111111111",
			BlockID: "22222222-2222-2222-2222-222222222222", PageNo: 18, Quote: "过温保护"}}})
	commitIntegrationSourceItems(t, ctx, datasets, source.ID, "A-2", "欠压锁定", "电压过低时禁止启动", model.ItemProvenance{})

	firstTask := createAndStartQueryTask(t, ctx, definitions, runtime, definition.ID, source.ID, target.ID, "Query Batch 1")
	taskIDs = append(taskIDs, firstTask.ID)
	waitIntegrationTaskStatus(t, ctx, repo, firstTask.ID, model.TaskStatusSucceeded)
	cursor, err := repo.GetPipelineCursor(ctx, "product_spec_query_v1", source.ID, target.ID)
	if err != nil || cursor.ProcessedThroughSeq != 2 || cursor.LastSuccessTaskID != firstTask.ID {
		t.Fatalf("首批 Cursor 错误: %+v err=%v", cursor, err)
	}
	queryItems, err := repo.ListDatasetItemsAfter(ctx, target.ID, 0, 2, 10)
	if err != nil || len(queryItems) != 2 {
		t.Fatalf("首批 Query Dataset 错误: items=%+v err=%v", queryItems, err)
	}
	var provenance model.ItemProvenance
	if err := json.Unmarshal([]byte(queryItems[0].Provenance), &provenance); err != nil ||
		provenance.SourceDatasetItemID == "" || provenance.SourceFingerprint == "" || len(provenance.SourceRefs) == 0 {
		t.Fatalf("Query Item lineage 不完整: %+v err=%v", provenance, err)
	}

	commitIntegrationSourceItems(t, ctx, datasets, source.ID, "A-3", "短路保护", "短路后立即关断", model.ItemProvenance{})
	secondTask := createAndStartQueryTask(t, ctx, definitions, runtime, definition.ID, source.ID, target.ID, "Query Batch 2")
	taskIDs = append(taskIDs, secondTask.ID)
	waitIntegrationTaskStatus(t, ctx, repo, secondTask.ID, model.TaskStatusSucceeded)
	cursor, err = repo.GetPipelineCursor(ctx, "product_spec_query_v1", source.ID, target.ID)
	if err != nil || cursor.ProcessedThroughSeq != 3 || cursor.LastSuccessTaskID != secondTask.ID {
		t.Fatalf("第二批 Cursor 错误: %+v err=%v", cursor, err)
	}
	targetState, _ := repo.GetAppendDataset(ctx, target.ID)
	if targetState.CurrentSeq != 3 || targetState.ItemCount != 3 {
		t.Fatalf("第二批不得重复派生 Batch 1: %+v", targetState)
	}
	secondExecution, err := repo.GetTaskExecution(ctx, secondTask.ID)
	if err != nil || len(secondExecution.StepOutputs) != 2 {
		t.Fatalf("阶段 D 必须输出 Batch + Cursor 一等资源: %+v err=%v", secondExecution, err)
	}
}

func TestIntegrationQueryDatasetBatchFailureDoesNotAdvanceCursor(t *testing.T) {
	ctx := context.Background()
	db, err := database.Connect(testDSN(), 3, 500)
	if err != nil {
		t.Skipf("本地 PG 不可用，跳过集成测试: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewPipelineRepo(db)
	datasets := apppipeline.NewDatasetService(repo)
	queries, _ := apppipeline.NewQueryDatasetService(repo, datasets)
	executor, _ := apppipeline.NewQueryDatasetDeriveExecutor(queries)
	registry, _ := apporchestrator.NewRegistry(executor)
	definitions := apporchestrator.NewDefinitionService(repo, registry, repo)
	scheduler := apporchestrator.NewScheduler(repo)
	worker, _ := apporchestrator.NewWorker(repo, registry, scheduler, apporchestrator.WorkerOptions{
		LeaseDuration: 2 * time.Second, PollInterval: 10 * time.Millisecond,
		RecoveryInterval: 100 * time.Millisecond, ReconcileLimit: 20,
	})
	runtime, _ := apporchestrator.NewRuntimeService(repo, scheduler, worker)
	workerCtx, cancelWorker := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Run(workerCtx) }()
	t.Cleanup(func() {
		cancelWorker()
		select {
		case <-workerDone:
		case <-time.After(2 * time.Second):
		}
	})

	source, target, schemaIDs := createIntegrationQueryDatasets(t, ctx, datasets)
	definition := createIntegrationQueryDefinition(t, ctx, definitions)
	var taskID string
	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM datasets WHERE id IN (?, ?)`, source.ID, target.ID).Error
		if taskID != "" {
			_ = db.Exec(`DELETE FROM tasks WHERE id = ?`, taskID).Error
		}
		_ = db.Exec(`DELETE FROM task_definitions WHERE id = ?`, definition.ID).Error
		for _, schemaID := range schemaIDs {
			_ = db.Exec(`DELETE FROM dataset_schemas WHERE id = ?`, schemaID).Error
		}
	})

	commitIntegrationSourceItems(t, ctx, datasets, source.ID, "C-1", "冲突条目", "用于制造目标主键冲突", model.ItemProvenance{})
	sourceItems, err := repo.ListDatasetItemsAfter(ctx, source.ID, 0, 1, 10)
	if err != nil || len(sourceItems) != 1 {
		t.Fatal(err)
	}
	preexisting, err := datasets.CreateBatch(ctx, apppipeline.CreateBatchInput{DatasetID: target.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = datasets.CommitBatch(ctx, preexisting.ID, []apppipeline.BatchItemInput{{Fields: integrationQueryFields(
		sourceItems[0], "冲突占位")}})
	if err != nil {
		t.Fatal(err)
	}

	task := createAndStartQueryTask(t, ctx, definitions, runtime, definition.ID, source.ID, target.ID, "Query Conflict")
	taskID = task.ID
	waitIntegrationTaskStatus(t, ctx, repo, task.ID, model.TaskStatusFailed)
	cursor, err := repo.GetPipelineCursor(ctx, "product_spec_query_v1", source.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.ProcessedThroughSeq != 0 || cursor.LastSuccessTaskID != "" {
		t.Fatalf("目标 Batch 冲突失败时 Cursor 不得前移: %+v", cursor)
	}
	targetState, _ := repo.GetAppendDataset(ctx, target.ID)
	if targetState.CurrentSeq != 1 || targetState.ItemCount != 1 {
		t.Fatalf("失败事务不得向 Query Dataset 追加部分记录: %+v", targetState)
	}
}

func createIntegrationQueryDatasets(t *testing.T, ctx context.Context,
	datasets *apppipeline.DatasetService) (*model.Dataset, *model.Dataset, []string) {
	t.Helper()
	baseSchema, err := datasets.CreateSchema(ctx, apppipeline.CreateSchemaInput{Name: "Stage D Base",
		JSONSchema: json.RawMessage(`{"type":"object","properties":{"sku":{"type":"string"},"name":{"type":"string"},"description":{"type":"string"},"aliases":{"type":"array","items":{"type":"string"}},"keywords":{"type":"string"},"product_family":{"type":"string"},"module":{"type":"string"}},"required":["sku","name","description","aliases","keywords","product_family","module"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	source, err := datasets.CreateDataset(ctx, apppipeline.CreateDatasetInput{Name: "Stage D Base", Purpose: model.DatasetPurposeBase,
		SchemaID: baseSchema.ID, KeyFields: []string{"sku"}})
	if err != nil {
		t.Fatal(err)
	}
	querySchema, err := datasets.CreateSchema(ctx, apppipeline.CreateSchemaInput{Name: "Stage D Query", JSONSchema: integrationQuerySchema()})
	if err != nil {
		t.Fatal(err)
	}
	target, err := datasets.CreateDataset(ctx, apppipeline.CreateDatasetInput{Name: "Stage D Query", Purpose: model.DatasetPurposeQuery,
		SchemaID: querySchema.ID, KeyFields: []string{"semantic_unit_key"}})
	if err != nil {
		t.Fatal(err)
	}
	return source, target, []string{baseSchema.ID, querySchema.ID}
}

func createIntegrationQueryDefinition(t *testing.T, ctx context.Context,
	definitions *apporchestrator.DefinitionService) *model.TaskDefinition {
	t.Helper()
	config, _ := json.Marshal(apppipeline.QueryDerivationConfig{PipelineKey: "product_spec_query_v1",
		TitleField: "name", DefinitionFields: []string{"description"}, AliasFields: []string{"aliases"},
		KeywordFields: []string{"keywords"}, FacetFields: map[string]string{
			"product_family": "product_family", "module": "module",
		}})
	definition, err := definitions.Create(ctx, model.TaskDefinition{Key: fmt.Sprintf("stage_d_%d", time.Now().UnixNano()),
		Name: "Stage D Query Derivation", Status: model.TaskDefinitionActive,
		InputPorts: map[string]model.PortDefinition{
			"source": {ResourceType: model.ResourceDatasetBoundary, Required: true},
			"target": {ResourceType: model.ResourceDataset, Required: true},
		}, OutputPorts: map[string]model.PortDefinition{
			"batch": {ResourceType: model.ResourceDatasetBatch}, "cursor": {ResourceType: model.ResourcePipelineCursor},
		}, OutputBindings: map[string]string{
			"batch": "$step.derive.batch", "cursor": "$step.derive.cursor",
		}, Steps: []model.StepDefinition{{ID: "derive", Name: "增量派生 Query Dataset",
			Kind: model.StepKindDataQueryDerive, Inputs: map[string]string{
				"source": "$task.source", "target": "$task.target",
			}, Outputs: map[string]model.ResourceType{
				"batch": model.ResourceDatasetBatch, "cursor": model.ResourcePipelineCursor,
			}, Config: config}}})
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func createAndStartQueryTask(t *testing.T, ctx context.Context, definitions *apporchestrator.DefinitionService,
	runtime *apporchestrator.RuntimeService, definitionID, sourceDatasetID, targetDatasetID, title string) *model.Task {
	t.Helper()
	task, err := definitions.CreateTask(ctx, apporchestrator.CreateTaskInput{DefinitionID: definitionID, Title: title,
		Bindings: []model.TaskResourceBinding{
			{PortName: "source", ResourceType: model.ResourceDatasetBoundary, ResourceID: sourceDatasetID},
			{PortName: "target", ResourceType: model.ResourceDataset, ResourceID: targetDatasetID},
		}})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	return task
}

func waitIntegrationTaskStatus(t *testing.T, ctx context.Context, repo *PipelineRepo, taskID, expected string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		execution, err := repo.GetTaskExecution(ctx, taskID)
		if err == nil && execution.Task.Status == expected {
			return
		}
		if err == nil && execution.Task.Status == model.TaskStatusFailed && expected != model.TaskStatusFailed {
			t.Fatalf("Task 提前失败: %s", execution.Task.ErrorMessage)
		}
		time.Sleep(20 * time.Millisecond)
	}
	execution, _ := repo.GetTaskExecution(ctx, taskID)
	t.Fatalf("等待 Task 状态 %s 超时: %+v", expected, execution)
}

func commitIntegrationSourceItems(t *testing.T, ctx context.Context, datasets *apppipeline.DatasetService,
	datasetID, sku, name, description string, provenance model.ItemProvenance) {
	t.Helper()
	batch, err := datasets.CreateBatch(ctx, apppipeline.CreateBatchInput{DatasetID: datasetID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = datasets.CommitBatch(ctx, batch.ID, []apppipeline.BatchItemInput{{Fields: map[string]any{
		"sku": sku, "name": name, "description": description, "aliases": []any{sku},
		"keywords": name + "，保护", "product_family": "X100", "module": "power",
	}, Provenance: provenance}})
	if err != nil {
		t.Fatal(err)
	}
}

func integrationQueryFields(source model.DatasetItem, title string) map[string]any {
	return map[string]any{
		"semantic_unit_key": source.ID + ":item", "source_item_id": source.ID,
		"source_fingerprint": source.Fingerprint, "title": title, "aliases": []any{},
		"definition": "占位", "keywords": []any{}, "facets": map[string]any{
			"product_family": "X100", "module": "power",
		}, "source_refs": []any{map[string]any{"dataset_item_id": source.ID}},
	}
}

func integrationQuerySchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
		"semantic_unit_key":{"type":"string"},"source_item_id":{"type":"string"},"source_fingerprint":{"type":"string"},
		"title":{"type":"string"},"aliases":{"type":"array","items":{"type":"string"}},"definition":{"type":"string"},
		"keywords":{"type":"array","items":{"type":"string"}},
		"facets":{"type":"object","properties":{"product_family":{"type":"string"},"module":{"type":"string"}},"additionalProperties":false},
		"source_refs":{"type":"array","items":{"type":"object","properties":{"dataset_item_id":{"type":"string"},"asset_id":{"type":"string"},"block_id":{"type":"string"},"page_no":{"type":"integer"},"quote":{"type":"string"}},"required":["dataset_item_id"],"additionalProperties":false}}
	},"required":["semantic_unit_key","source_item_id","source_fingerprint","title","aliases","definition","keywords","facets","source_refs"],"additionalProperties":false}`)
}
