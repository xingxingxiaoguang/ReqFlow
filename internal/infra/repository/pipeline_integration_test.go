//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

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
	registry, registryErr := appOrchestrator.NewRegistry(
		integrationExecutor{kind: model.StepKindDocumentExtract},
		integrationExecutor{kind: model.StepKindDataPublish},
	)
	if registryErr != nil {
		t.Fatal(registryErr)
	}
	service := appOrchestrator.NewDefinitionService(repo, registry, integrationResourceResolver{})
	definition, err := service.Create(ctx, model.TaskDefinition{
		Key: "integration_clean", Name: "集成清洗", Status: model.TaskDefinitionActive,
		InputPorts: map[string]model.PortDefinition{
			"documents": {ResourceType: model.ResourceAssetSet, Required: true},
			"target":    {ResourceType: model.ResourceDataset, Required: true},
		},
		OutputPorts:    map[string]model.PortDefinition{"batch": {ResourceType: model.ResourceDatasetBatch}},
		OutputBindings: map[string]string{"batch": "$step.publish.batch"},
		Steps: []model.StepDefinition{
			{ID: "extract", Name: "抽取", Kind: model.StepKindDocumentExtract,
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

func TestIntegrationCreateTaskExecutionsPersistsBatchIsolation(t *testing.T) {
	ctx := context.Background()
	db, err := database.Connect(testDSN(), 3, 500)
	if err != nil {
		t.Skipf("本地 PG 不可用，跳过集成测试: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	repo := NewPipelineRepo(db)
	registry, err := appOrchestrator.NewRegistry(integrationExecutor{kind: model.StepKindDocumentExtract})
	if err != nil {
		t.Fatal(err)
	}
	definitions := appOrchestrator.NewDefinitionService(repo, registry, integrationResourceResolver{})
	definition, err := definitions.Create(ctx, model.TaskDefinition{
		Key: fmt.Sprintf("batch_integration_%d", time.Now().UnixNano()), Name: "按文件拆分集成测试",
		Status: model.TaskDefinitionActive,
		InputPorts: map[string]model.PortDefinition{
			"documents": {ResourceType: model.ResourceAssetSet, Required: true},
		},
		OutputPorts:    map[string]model.PortDefinition{"drafts": {ResourceType: model.ResourceRecordDrafts}},
		OutputBindings: map[string]string{"drafts": "$step.extract.drafts"},
		Steps: []model.StepDefinition{{ID: "extract", Name: "抽取", Kind: model.StepKindDocumentExtract,
			Inputs:  map[string]string{"documents": "$task.documents"},
			Outputs: map[string]model.ResourceType{"drafts": model.ResourceRecordDrafts}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	batchID := uuid.NewString()
	assets := make([]*model.Asset, 2)
	assetSets := make([]*model.AssetSet, 2)
	filenames := []string{"需求说明.docx", "产品清单.xlsx"}
	for i, filename := range filenames {
		asset, _, createErr := repo.CreateOrGetAsset(ctx, &model.Asset{
			WorkspaceID: "default", Filename: filename, MIMEType: "application/octet-stream",
			SizeBytes: int64(i + 1), SHA256: fmt.Sprintf("batch-integration-%d-%d", time.Now().UnixNano(), i),
			BlobURI: "file:///integration/" + filename,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		assets[i] = asset
		assetSet := &model.AssetSet{WorkspaceID: "default", Name: "单文件 · " + filename, CreatedBy: "task_batch:" + batchID}
		if err := repo.CreateAssetSet(ctx, assetSet, []model.AssetSetMember{{AssetID: asset.ID, Ordinal: 0}}); err != nil {
			t.Fatal(err)
		}
		assetSets[i] = assetSet
	}

	snapshot, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	executions := make([]port.TaskExecutionCreate, len(assets))
	for i := range assets {
		executions[i] = port.TaskExecutionCreate{
			Task: &model.Task{
				WorkspaceID: "default", DefinitionID: definition.ID, DefinitionSnapshot: string(snapshot),
				Type: "orchestrator", Title: "批量抽取 · " + filenames[i], Status: model.TaskStatusPending,
				BatchID: batchID, BatchOrdinal: i + 1, BatchSize: len(assets),
				SourceAssetID: assets[i].ID, SourceFilename: filenames[i],
			},
			Bindings: []model.TaskResourceBinding{{
				PortName: "documents", Direction: model.ResourceInput,
				ResourceType: model.ResourceAssetSet, ResourceID: assetSets[i].ID,
			}},
			Steps: []model.StepRun{{
				StepID: "extract", Ordinal: 1, Kind: model.StepKindDocumentExtract, Status: model.StepRunPending,
			}},
		}
	}
	t.Cleanup(func() {
		for i := range executions {
			_ = db.Exec(`DELETE FROM tasks WHERE id = ?`, executions[i].Task.ID).Error
		}
		for i := range assetSets {
			_ = db.Exec(`DELETE FROM asset_sets WHERE id = ?`, assetSets[i].ID).Error
			_ = db.Exec(`DELETE FROM assets WHERE id = ?`, assets[i].ID).Error
		}
		_ = db.Exec(`DELETE FROM task_definitions WHERE id = ?`, definition.ID).Error
	})

	if err := repo.CreateTaskExecutions(ctx, executions); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ListDatasetSchemas(ctx, "default", 10); err != nil {
		t.Fatalf("内部文件集过滤不能影响数据结构列表: %v", err)
	}
	visibleSets, err := repo.ListAssetSets(ctx, "default", 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, visibleSet := range visibleSets {
		if visibleSet.ID == assetSets[0].ID || visibleSet.ID == assetSets[1].ID {
			t.Fatalf("任务批次内部单文件集不应出现在普通文件集列表: %s", visibleSet.ID)
		}
	}
	if executions[0].Task.ID == executions[1].Task.ID {
		t.Fatal("每个文件必须生成不同任务实例")
	}
	for i := range executions {
		stored, err := repo.GetTaskExecution(ctx, executions[i].Task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Task.BatchID != batchID || stored.Task.BatchOrdinal != i+1 || stored.Task.BatchSize != len(assets) {
			t.Fatalf("第 %d 个子任务批次信息错误: %+v", i+1, stored.Task)
		}
		if stored.Task.SourceAssetID != assets[i].ID || stored.Task.SourceFilename != filenames[i] {
			t.Fatalf("第 %d 个子任务来源文件错误: %+v", i+1, stored.Task)
		}
		if len(stored.Inputs) != 1 || stored.Inputs[0].ResourceID != assetSets[i].ID {
			t.Fatalf("第 %d 个子任务未绑定独立单文件集: %+v", i+1, stored.Inputs)
		}
		if len(stored.Steps) != 1 || stored.Steps[0].TaskID != stored.Task.ID {
			t.Fatalf("第 %d 个子任务步骤实例串线: %+v", i+1, stored.Steps)
		}
	}
	if executions[0].Bindings[0].ResourceID == executions[1].Bindings[0].ResourceID {
		t.Fatal("不同文件的子任务不能共享同一个输入文件集")
	}
}

func TestIntegrationOrchestratorWorkerHumanGateAndLeaseFencing(t *testing.T) {
	ctx := context.Background()
	db, err := database.Connect(testDSN(), 3, 500)
	if err != nil {
		t.Skipf("本地 PG 不可用，跳过集成测试: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	repo := NewPipelineRepo(db)
	registry, err := appOrchestrator.NewRegistry(
		flowExecutor{kind: model.StepKindDocumentExtract},
		flowExecutor{kind: model.StepKindDataPublish},
	)
	if err != nil {
		t.Fatal(err)
	}
	definitions := appOrchestrator.NewDefinitionService(repo, registry, integrationResourceResolver{})
	definition, err := definitions.Create(ctx, model.TaskDefinition{
		Key: fmt.Sprintf("runtime_%d", time.Now().UnixNano()), Name: "运行时集成", Status: model.TaskDefinitionActive,
		InputPorts: map[string]model.PortDefinition{
			"documents": {ResourceType: model.ResourceAssetSet, Required: true},
			"target":    {ResourceType: model.ResourceDataset, Required: true},
		},
		OutputPorts: map[string]model.PortDefinition{
			"batch":  {ResourceType: model.ResourceDatasetBatch},
			"report": {ResourceType: model.ResourceArtifact},
		},
		OutputBindings: map[string]string{"batch": "$step.publish.batch", "report": "$step.publish.report"},
		Steps: []model.StepDefinition{
			{ID: "extract", Name: "初次抽取", Kind: model.StepKindDocumentExtract,
				Inputs:  map[string]string{"documents": "$task.documents"},
				Outputs: map[string]model.ResourceType{"drafts": model.ResourceRecordDrafts}},
			{ID: "refine", Name: "二次抽取", Kind: model.StepKindDocumentExtract, DependsOn: []string{"extract"},
				Inputs:  map[string]string{"drafts": "$step.extract.drafts"},
				Outputs: map[string]model.ResourceType{"drafts": model.ResourceRecordDrafts}},
			{ID: "review", Name: "任意位置审核", Kind: model.StepKindHumanReview, DependsOn: []string{"refine"},
				Inputs:  map[string]string{"drafts": "$step.refine.drafts"},
				Outputs: map[string]model.ResourceType{"approved": model.ResourceApprovedRecords}},
			{ID: "publish", Name: "发布", Kind: model.StepKindDataPublish, DependsOn: []string{"review"},
				Inputs:  map[string]string{"records": "$step.review.approved", "dataset": "$task.target"},
				Outputs: map[string]model.ResourceType{"batch": model.ResourceDatasetBatch, "report": model.ResourceArtifact}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := definitions.CreateTask(ctx, appOrchestrator.CreateTaskInput{
		DefinitionID: definition.ID,
		Bindings: []model.TaskResourceBinding{
			{PortName: "documents", ResourceType: model.ResourceAssetSet, ResourceID: "11111111-1111-1111-1111-111111111111"},
			{PortName: "target", ResourceType: model.ResourceDataset, ResourceID: "22222222-2222-2222-2222-222222222222"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM tasks WHERE id = ?`, task.ID).Error
		_ = db.Exec(`DELETE FROM task_definitions WHERE id = ?`, definition.ID).Error
	})

	scheduler := appOrchestrator.NewScheduler(repo)
	worker, err := appOrchestrator.NewWorker(repo, registry, scheduler, appOrchestrator.WorkerOptions{
		Owner: "integration-worker", LeaseDuration: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := appOrchestrator.NewRuntimeService(repo, scheduler, worker)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(ctx); err != nil { // extract
		t.Fatal(err)
	}
	if err := worker.RunOnce(ctx); err != nil { // refine（同 Kind，不串输出）
		t.Fatal(err)
	}
	execution, err := repo.GetTaskExecution(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Task.Status != model.TaskStatusAwaiting || execution.Steps[2].Status != model.StepRunAwaiting {
		t.Fatalf("任意位置 Human Gate 未进入 awaiting: task=%s steps=%+v", execution.Task.Status, execution.Steps)
	}
	if len(execution.StepOutputs) != 2 || execution.StepOutputs[0].StepRunID == execution.StepOutputs[1].StepRunID {
		t.Fatalf("重复 Kind 的输出必须按 StepRun 隔离: %+v", execution.StepOutputs)
	}
	if err := runtime.ApproveHumanStep(ctx, task.ID, "review", appOrchestrator.StepResult{Outputs: map[string]model.ResourceRef{
		"approved": {ResourceType: model.ResourceApprovedRecords, ResourceID: "55555555-5555-5555-5555-555555555555"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(ctx); err != nil { // publish
		t.Fatal(err)
	}
	execution, err = repo.GetTaskExecution(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Task.Status != model.TaskStatusSucceeded {
		t.Fatalf("任务未完成: %+v", execution.Task)
	}
	var taskOutputCount int64
	if err := db.Raw(`SELECT count(*) FROM task_resource_bindings WHERE task_id = ? AND direction = 'output'`,
		task.ID).Scan(&taskOutputCount).Error; err != nil || taskOutputCount != 2 {
		t.Fatalf("任务输出绑定未固化: count=%d err=%v", taskOutputCount, err)
	}

	// owner fencing：错误 owner 不能续租或完成，旧 Worker 不会覆盖新 Worker。
	definition2 := *definition
	definition2.ID = ""
	definition2.Key = fmt.Sprintf("lease_%d", time.Now().UnixNano())
	definition2.Steps = definition2.Steps[:1]
	definition2.OutputPorts = map[string]model.PortDefinition{"drafts": {ResourceType: model.ResourceRecordDrafts}}
	definition2.OutputBindings = map[string]string{"drafts": "$step.extract.drafts"}
	definition2, err = derefDefinition(definitions.Create(ctx, definition2))
	if err != nil {
		t.Fatal(err)
	}
	leaseTask, err := definitions.CreateTask(ctx, appOrchestrator.CreateTaskInput{
		DefinitionID: definition2.ID,
		Bindings: []model.TaskResourceBinding{
			{PortName: "documents", ResourceType: model.ResourceAssetSet, ResourceID: "11111111-1111-1111-1111-111111111111"},
			{PortName: "target", ResourceType: model.ResourceDataset, ResourceID: "22222222-2222-2222-2222-222222222222"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM tasks WHERE id = ?`, leaseTask.ID).Error
		_ = db.Exec(`DELETE FROM task_definitions WHERE id = ?`, definition2.ID).Error
	})
	if err := runtime.Start(ctx, leaseTask.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimStep(ctx, "owner-a", time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RenewStepLease(ctx, claimed.ID, "owner-b", time.Now().Add(5*time.Second)); !errors.Is(err, port.ErrLeaseLost) {
		t.Fatalf("错误 owner 续租应被 fencing: %v", err)
	}
	if err := repo.CompleteClaimedStep(ctx, claimed.ID, "owner-b", nil); !errors.Is(err, port.ErrLeaseLost) {
		t.Fatalf("错误 owner 完成应被 fencing: %v", err)
	}
	if err := runtime.Pause(ctx, leaseTask.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveStepCheckpoint(ctx, claimed.ID, "owner-a", json.RawMessage(`{"offset":7}`)); err != nil {
		t.Fatalf("pausing 期间有效 owner 应能保存最后检查点: %v", err)
	}
	if err := repo.RenewStepLease(ctx, claimed.ID, "owner-a", time.Now().Add(5*time.Second)); !errors.Is(err, port.ErrPauseRequested) {
		t.Fatalf("远端 Worker 续租应收到暂停请求: %v", err)
	}
	if err := repo.PauseClaimedStep(ctx, claimed.ID, "owner-a"); err != nil {
		t.Fatal(err)
	}
	paused, err := repo.GetTaskExecution(ctx, leaseTask.ID)
	if err != nil || paused.Task.Status != model.TaskStatusPaused || paused.Steps[0].Status != model.StepRunPaused {
		t.Fatalf("暂停未收敛: execution=%+v err=%v", paused, err)
	}
	if err := runtime.Resume(ctx, leaseTask.ID); err != nil {
		t.Fatal(err)
	}
	expiredClaim, err := repo.ClaimStep(ctx, "owner-expired", time.Now().Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if string(expiredClaim.Checkpoint) != `{"offset": 7}` && string(expiredClaim.Checkpoint) != `{"offset":7}` {
		t.Fatalf("恢复后检查点丢失: %s", expiredClaim.Checkpoint)
	}
	if recovered, err := repo.RecoverExpiredLeases(ctx); err != nil || recovered != 1 {
		t.Fatalf("过期 lease 未回收: recovered=%d err=%v", recovered, err)
	}
	newClaim, err := repo.ClaimStep(ctx, "owner-new", time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if newClaim.Attempt != expiredClaim.Attempt+1 {
		t.Fatalf("恢复后 attempt 未递增: old=%d new=%d", expiredClaim.Attempt, newClaim.Attempt)
	}
	if err := repo.CompleteClaimedStep(ctx, newClaim.ID, "owner-expired", nil); !errors.Is(err, port.ErrLeaseLost) {
		t.Fatalf("lease 回收后旧 owner 必须失效: %v", err)
	}
}

func derefDefinition(definition *model.TaskDefinition, err error) (model.TaskDefinition, error) {
	if err != nil {
		return model.TaskDefinition{}, err
	}
	return *definition, nil
}

type flowExecutor struct{ kind model.StepKind }

func (e flowExecutor) Kind() model.StepKind                                         { return e.kind }
func (flowExecutor) ValidateDefinition(context.Context, model.StepDefinition) error { return nil }
func (e flowExecutor) Execute(_ context.Context, run appOrchestrator.StepRunContext) (appOrchestrator.StepResult, error) {
	switch run.StepID {
	case "extract":
		return appOrchestrator.StepResult{Outputs: map[string]model.ResourceRef{
			"drafts": {ResourceType: model.ResourceRecordDrafts, ResourceID: "33333333-3333-3333-3333-333333333333"},
		}}, nil
	case "refine":
		if run.Inputs["drafts"].ResourceID != "33333333-3333-3333-3333-333333333333" {
			return appOrchestrator.StepResult{}, fmt.Errorf("refine 输入串线: %+v", run.Inputs)
		}
		return appOrchestrator.StepResult{Outputs: map[string]model.ResourceRef{
			"drafts": {ResourceType: model.ResourceRecordDrafts, ResourceID: "44444444-4444-4444-4444-444444444444"},
		}}, nil
	case "publish":
		if run.Inputs["records"].ResourceID != "55555555-5555-5555-5555-555555555555" {
			return appOrchestrator.StepResult{}, fmt.Errorf("publish 输入错误: %+v", run.Inputs)
		}
		return appOrchestrator.StepResult{Outputs: map[string]model.ResourceRef{
			"batch":  {ResourceType: model.ResourceDatasetBatch, ResourceID: "66666666-6666-6666-6666-666666666666"},
			"report": {ResourceType: model.ResourceArtifact, ResourceID: "88888888-8888-8888-8888-888888888888"},
		}}, nil
	default:
		return appOrchestrator.StepResult{}, fmt.Errorf("未知步骤 %s", run.StepID)
	}
}
func (e flowExecutor) Resume(ctx context.Context, run appOrchestrator.StepRunContext, _ json.RawMessage) (appOrchestrator.StepResult, error) {
	return e.Execute(ctx, run)
}

type integrationExecutor struct{ kind model.StepKind }

type integrationResourceResolver struct{}

func (integrationResourceResolver) ResolveTaskResource(_ context.Context, _ string, binding model.TaskResourceBinding, _ string) (model.TaskResourceBinding, error) {
	return binding, nil
}

func (e integrationExecutor) Kind() model.StepKind { return e.kind }
func (integrationExecutor) ValidateDefinition(context.Context, model.StepDefinition) error {
	return nil
}
func (integrationExecutor) Execute(context.Context, appOrchestrator.StepRunContext) (appOrchestrator.StepResult, error) {
	return appOrchestrator.StepResult{}, nil
}
func (integrationExecutor) Resume(context.Context, appOrchestrator.StepRunContext, json.RawMessage) (appOrchestrator.StepResult, error) {
	return appOrchestrator.StepResult{}, nil
}
