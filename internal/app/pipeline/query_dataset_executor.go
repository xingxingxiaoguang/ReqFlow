package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reqflow/internal/app/orchestrator"
	"reqflow/internal/domain/model"
)

// QueryDatasetDeriveExecutor 只接收固化了 through_seq 的 Base Dataset Boundary，
// 并通过 QueryDatasetService 原子提交目标 Batch + PipelineCursor。
type QueryDatasetDeriveExecutor struct {
	queries *QueryDatasetService
}

func NewQueryDatasetDeriveExecutor(queries *QueryDatasetService) (*QueryDatasetDeriveExecutor, error) {
	if queries == nil {
		return nil, fmt.Errorf("data.query_derive: query dataset service is required")
	}
	return &QueryDatasetDeriveExecutor{queries: queries}, nil
}

func (*QueryDatasetDeriveExecutor) Kind() model.StepKind { return model.StepKindDataQueryDerive }

func (*QueryDatasetDeriveExecutor) ValidateDefinition(_ context.Context, step model.StepDefinition) error {
	if len(step.Inputs) != 2 || strings.TrimSpace(step.Inputs["source"]) == "" ||
		strings.TrimSpace(step.Inputs["target"]) == "" {
		return fmt.Errorf("data.query_derive 必须且只能声明 source 与 target 输入")
	}
	if len(step.Outputs) != 2 || step.Outputs["batch"] != model.ResourceDatasetBatch ||
		step.Outputs["cursor"] != model.ResourcePipelineCursor {
		return fmt.Errorf("data.query_derive 必须声明 batch: dataset_batch 与 cursor: pipeline_cursor 输出")
	}
	_, err := DecodeQueryDerivationConfig(step.Config)
	return err
}

func (e *QueryDatasetDeriveExecutor) Execute(ctx context.Context,
	run orchestrator.StepRunContext) (orchestrator.StepResult, error) {
	source, ok := run.Inputs["source"]
	if !ok || source.ResourceType != model.ResourceDatasetBoundary || strings.TrimSpace(source.ResourceID) == "" {
		return orchestrator.StepResult{}, fmt.Errorf("data.query_derive source 必须是 Dataset Boundary")
	}
	target, ok := run.Inputs["target"]
	if !ok || target.ResourceType != model.ResourceDataset || strings.TrimSpace(target.ResourceID) == "" {
		return orchestrator.StepResult{}, fmt.Errorf("data.query_derive target 必须是具体 Query Dataset")
	}
	var sourceBoundary model.DatasetBoundary
	if err := json.Unmarshal(source.Boundary, &sourceBoundary); err != nil {
		return orchestrator.StepResult{}, fmt.Errorf("解析 Base Dataset Boundary: %w", err)
	}
	if sourceBoundary.DatasetID != source.ResourceID || sourceBoundary.ThroughSeq <= 0 {
		return orchestrator.StepResult{}, fmt.Errorf("Base Dataset Boundary 与资源引用不一致或位点为空")
	}
	config, err := DecodeQueryDerivationConfig(run.Config)
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	derived, err := e.queries.Derive(ctx, DeriveQueryDatasetInput{
		SourceDatasetID: source.ResourceID, SourceThroughSeq: sourceBoundary.ThroughSeq,
		TargetDatasetID: target.ResourceID, Config: config,
		SourceTaskID: run.TaskID, SourceStepRunID: run.StepRunID, ProducerAttempt: run.Attempt,
	})
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	progress, _ := json.Marshal(map[string]any{
		"phase": "query_dataset_committed", "pipeline_key": config.PipelineKey,
		"source_items": derived.SourceItemCount, "query_items": derived.QueryItemCount,
		"processed_through_seq": derived.Cursor.ProcessedThroughSeq,
		"batch_id":              derived.Batch.ID, "from_seq": derived.Batch.FromSeq, "to_seq": derived.Batch.ToSeq,
	})
	if err := run.Progress.Report(ctx, progress); err != nil {
		return orchestrator.StepResult{}, err
	}
	batchBoundary, _ := json.Marshal(model.DatasetBatchBoundary{DatasetID: derived.Batch.DatasetID,
		FromSeq: derived.Batch.FromSeq, ToSeq: derived.Batch.ToSeq})
	cursorBoundary, _ := json.Marshal(model.PipelineCursorBoundary{
		PipelineKey: config.PipelineKey, SourceDatasetID: source.ResourceID,
		TargetDatasetID: target.ResourceID, ProcessedThroughSeq: sourceBoundary.ThroughSeq,
		TargetBatchID: derived.Batch.ID, TargetThroughSeq: derived.Batch.ToSeq,
	})
	return orchestrator.StepResult{Outputs: map[string]model.ResourceRef{
		"batch":  {ResourceType: model.ResourceDatasetBatch, ResourceID: derived.Batch.ID, Boundary: batchBoundary},
		"cursor": {ResourceType: model.ResourcePipelineCursor, ResourceID: derived.Cursor.ID, Boundary: cursorBoundary},
	}, Metrics: map[string]any{
		"source_items": derived.SourceItemCount, "query_items": derived.QueryItemCount,
		"processed_through_seq": derived.Cursor.ProcessedThroughSeq,
	}}, nil
}

func (e *QueryDatasetDeriveExecutor) Resume(ctx context.Context, run orchestrator.StepRunContext,
	_ json.RawMessage) (orchestrator.StepResult, error) {
	// Batch 以 StepRun 为幂等键；已提交时服务会校验 payload/Cursor 后直接复用。
	return e.Execute(ctx, run)
}
