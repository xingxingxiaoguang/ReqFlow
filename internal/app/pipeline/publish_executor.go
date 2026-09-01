package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reqflow/internal/app/orchestrator"
	"reqflow/internal/domain/model"
)

type DataPublishExecutor struct {
	publish *PublishService
}

func NewDataPublishExecutor(publish *PublishService) (*DataPublishExecutor, error) {
	if publish == nil {
		return nil, fmt.Errorf("data.publish: publish service is required")
	}
	return &DataPublishExecutor{publish: publish}, nil
}

func (*DataPublishExecutor) Kind() model.StepKind { return model.StepKindDataPublish }

func (*DataPublishExecutor) ValidateDefinition(_ context.Context, step model.StepDefinition) error {
	if len(step.Inputs) != 1 || strings.TrimSpace(step.Inputs["approved"]) == "" {
		return fmt.Errorf("data.publish 必须且只能声明 approved 输入")
	}
	if step.Outputs["batch"] != model.ResourceDatasetBatch {
		return fmt.Errorf("data.publish 必须声明 batch: dataset_batch 输出")
	}
	// 可选第二输出 dataset: dataset_boundary（发布后的数据集上界）。有了它，
	// retrieval.build 才能接在发布之后，索引到本任务刚提交的数据；否则清洗入库
	// 流程只能在任务创建时对旧边界建索引，永远覆盖不了自己发布的数据。
	if len(step.Outputs) > 2 || (len(step.Outputs) == 2 && step.Outputs["dataset"] != model.ResourceDatasetBoundary) {
		return fmt.Errorf("data.publish 可选第二输出只能是 dataset: dataset_boundary")
	}
	return validateEmptyExecutorConfig("data.publish", step.Config)
}

func (e *DataPublishExecutor) Execute(ctx context.Context, run orchestrator.StepRunContext) (orchestrator.StepResult, error) {
	approved, ok := run.Inputs["approved"]
	if !ok || approved.ResourceType != model.ResourceApprovedRecords || strings.TrimSpace(approved.ResourceID) == "" {
		return orchestrator.StepResult{}, fmt.Errorf("data.publish approved 输入必须是具体 ApprovedRecordSet")
	}
	batch, err := e.publish.PublishApprovedRecords(ctx, PublishApprovedRecordsInput{
		ApprovedRecordSetID: approved.ResourceID, SourceTaskID: run.TaskID,
		SourceStepRunID: run.StepRunID, ProducerAttempt: run.Attempt})
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	progress, _ := json.Marshal(map[string]any{"phase": "published", "dataset_id": batch.DatasetID,
		"batch_id": batch.ID, "item_count": batch.ItemCount, "from_seq": batch.FromSeq, "to_seq": batch.ToSeq})
	if err := run.Progress.Report(ctx, progress); err != nil {
		return orchestrator.StepResult{}, err
	}
	boundary, _ := json.Marshal(model.DatasetBatchBoundary{DatasetID: batch.DatasetID,
		FromSeq: batch.FromSeq, ToSeq: batch.ToSeq})
	outputs := map[string]model.ResourceRef{
		"batch": {ResourceType: model.ResourceDatasetBatch, ResourceID: batch.ID, Boundary: boundary},
	}
	if _, declared := run.Outputs["dataset"]; declared {
		datasetBoundary, _ := json.Marshal(model.DatasetBoundary{DatasetID: batch.DatasetID, ThroughSeq: batch.ToSeq})
		outputs["dataset"] = model.ResourceRef{ResourceType: model.ResourceDatasetBoundary,
			ResourceID: batch.DatasetID, Boundary: datasetBoundary}
	}
	return orchestrator.StepResult{Outputs: outputs, Metrics: map[string]any{"status": batch.Status, "items": batch.ItemCount,
		"from_seq": batch.FromSeq, "to_seq": batch.ToSeq}}, nil
}

func (e *DataPublishExecutor) Resume(ctx context.Context, run orchestrator.StepRunContext,
	_ json.RawMessage) (orchestrator.StepResult, error) {
	return e.Execute(ctx, run)
}
