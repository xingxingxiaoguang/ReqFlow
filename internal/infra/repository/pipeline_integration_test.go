//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	appOrchestrator "reqflow/internal/app/orchestrator"
	appPipeline "reqflow/internal/app/pipeline"
	"reqflow/internal/domain/model"
	"reqflow/internal/infra/database"
	"reqflow/internal/port"
)

func TestIntegrationPipelineAppendBatches(t *testing.T) {
	ctx := context.Background()
	db, err := database.Connect(testDSN(), 3, 500)
	if err != nil {
		t.Skipf("本地 PG 不可用，跳过集成测试: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	repo := NewPipelineRepo(db)
	service := appPipeline.NewDatasetService(repo)
	schema, err := service.CreateSchema(ctx, appPipeline.CreateSchemaInput{
		Name: "V2 集成规格",
		JSONSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"sku":{"type":"string"},"name":{"type":"string"}},
			"required":["sku","name"]
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := service.CreateDataset(ctx, appPipeline.CreateDatasetInput{
		Name: "V2 集成数据集", Purpose: model.DatasetPurposeBase,
		SchemaID: schema.ID, KeyFields: []string{"sku"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM outbox_events WHERE aggregate_id = ?`, dataset.ID).Error
		_ = db.Exec(`DELETE FROM datasets WHERE id = ?`, dataset.ID).Error
		_ = db.Exec(`DELETE FROM dataset_schemas WHERE id = ?`, schema.ID).Error
	})

	batch1, err := service.CreateBatch(ctx, appPipeline.CreateBatchInput{DatasetID: dataset.ID})
	if err != nil {
		t.Fatal(err)
	}
	got1, err := service.CommitBatch(ctx, batch1.ID, []appPipeline.BatchItemInput{
		{Fields: map[string]any{"sku": "B", "name": "第二条"}},
		{Fields: map[string]any{"sku": "A", "name": "第一条"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got1.FromSeq != 1 || got1.ToSeq != 2 {
		t.Fatalf("Batch1 位点错误: %+v", got1)
	}

	// 同一 Batch 重试不会重复插入或前移 current_seq。
	if _, err := service.CommitBatch(ctx, batch1.ID, []appPipeline.BatchItemInput{
		{Fields: map[string]any{"sku": "A", "name": "第一条"}},
		{Fields: map[string]any{"sku": "B", "name": "第二条"}},
	}); err != nil {
		t.Fatalf("Batch1 幂等重试失败: %v", err)
	}

	batch2, _ := service.CreateBatch(ctx, appPipeline.CreateBatchInput{DatasetID: dataset.ID})
	got2, err := service.CommitBatch(ctx, batch2.ID, []appPipeline.BatchItemInput{
		{Fields: map[string]any{"sku": "C", "name": "第三条"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got2.FromSeq != 3 || got2.ToSeq != 3 {
		t.Fatalf("Batch2 位点错误: %+v", got2)
	}

	items, err := repo.ListDatasetItemsAfter(ctx, dataset.ID, 1, 3, 10)
	if err != nil || len(items) != 2 || items[0].CommitSeq != 2 || items[1].CommitSeq != 3 {
		t.Fatalf("增量读取错误: %+v, %v", items, err)
	}
	current, err := repo.GetAppendDataset(ctx, dataset.ID)
	if err != nil || current.CurrentSeq != 3 || current.ItemCount != 3 {
		t.Fatalf("Dataset 汇总错误: %+v, %v", current, err)
	}

	duplicate, _ := service.CreateBatch(ctx, appPipeline.CreateBatchInput{DatasetID: dataset.ID})
	_, err = service.CommitBatch(ctx, duplicate.ID, []appPipeline.BatchItemInput{
		{Fields: map[string]any{"sku": "A", "name": "冲突"}},
	})
	if !errors.Is(err, port.ErrDatasetItemKeyConflict) {
		t.Fatalf("重复业务键应冲突，got %v", err)
	}

	var outboxCount int64
	if err := db.Raw(`SELECT count(*) FROM outbox_events WHERE aggregate_id = ? AND topic = 'dataset.batch_committed'`, dataset.ID).
		Scan(&outboxCount).Error; err != nil || outboxCount != 2 {
		t.Fatalf("每个成功 Batch 应产生一个 Outbox 事件: count=%d err=%v", outboxCount, err)
	}
}

func TestIntegrationTaskDefinitionAndResourceBindings(t *testing.T) {
	ctx := context.Background()
	db, err := database.Connect(testDSN(), 3, 500)
	if err != nil {
		t.Skipf("本地 PG 不可用，跳过集成测试: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	repo := NewPipelineRepo(db)
	service := appOrchestrator.NewDefinitionService(repo)
	definition, err := service.Create(ctx, model.TaskDefinition{
		Key: "integration_clean", Name: "集成清洗", Status: model.TaskDefinitionActive,
		InputPorts: map[string]model.PortDefinition{
			"documents": {ResourceType: model.ResourceAssetSet, Required: true},
			"target":    {ResourceType: model.ResourceDataset, Required: true},
		},
		OutputPorts:    map[string]model.PortDefinition{"batch": {ResourceType: model.ResourceDatasetBatch}},
		OutputBindings: map[string]string{"batch": "$step.publish.batch"},
		Steps: []model.StepDefinition{
			{ID: "extract", Name: "抽取", Kind: model.StepKindLLMExtract,
				Inputs:  map[string]string{"documents": "$task.documents"},
				Outputs: map[string]model.ResourceType{"drafts": model.ResourceRecordDrafts}},
			{ID: "review", Name: "审核", Kind: model.StepKindHumanReview, DependsOn: []string{"extract"},
				Inputs:  map[string]string{"drafts": "$step.extract.drafts"},
				Outputs: map[string]model.ResourceType{"approved": model.ResourceApprovedRecords}},
			{ID: "publish", Name: "提交", Kind: model.StepKindDataPublish, DependsOn: []string{"review"},
				Inputs:  map[string]string{"records": "$step.review.approved", "dataset": "$task.target"},
				Outputs: map[string]model.ResourceType{"batch": model.ResourceDatasetBatch}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	task, err := service.CreateTask(ctx, appOrchestrator.CreateTaskInput{
		DefinitionID: definition.ID,
		Bindings: []model.TaskResourceBinding{
			{PortName: "documents", ResourceType: model.ResourceAssetSet, ResourceID: "11111111-1111-1111-1111-111111111111"},
			{PortName: "target", ResourceType: model.ResourceDataset, ResourceID: "22222222-2222-2222-2222-222222222222",
				Boundary: json.RawMessage(`{"through_seq":12}`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM tasks WHERE id = ?`, task.ID).Error
		_ = db.Exec(`DELETE FROM task_definitions WHERE id = ?`, definition.ID).Error
	})

	bindings, err := repo.GetTaskResourceBindings(ctx, task.ID)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("资源绑定错误: %+v, %v", bindings, err)
	}
	steps, err := repo.GetStepRuns(ctx, task.ID)
	if err != nil || len(steps) != 3 {
		t.Fatalf("StepRun 错误: %+v, %v", steps, err)
	}
	if steps[0].StepID != "extract" || steps[1].StepID != "review" || steps[2].StepID != "publish" {
		t.Fatalf("StepRun 必须按定义步骤身份持久化: %+v", steps)
	}

	var snapshot string
	if err := db.Raw(`SELECT definition_snapshot::text FROM tasks WHERE id = ?`, task.ID).Scan(&snapshot).Error; err != nil || snapshot == "" {
		t.Fatalf("Task 定义快照缺失: %q, %v", snapshot, err)
	}
}
