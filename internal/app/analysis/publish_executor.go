package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"reqflow/internal/app/orchestrator"
	"reqflow/internal/app/pipeline"
	"reqflow/internal/domain/model"
)

// AnalysisPublishExecutor 把 AnalysisProfile 输出中的一个记录数组发布到目标 Dataset。
// 它是通用 JSON → DatasetBatch 适配器，不包含 Bug/图谱等业务分支。
type AnalysisPublishExecutor struct {
	analysis *Service
	datasets *pipeline.DatasetService
}

func NewAnalysisPublishExecutor(analysis *Service, datasets *pipeline.DatasetService) (*AnalysisPublishExecutor, error) {
	if analysis == nil || datasets == nil {
		return nil, fmt.Errorf("data.analysis_publish 依赖不完整")
	}
	return &AnalysisPublishExecutor{analysis: analysis, datasets: datasets}, nil
}

func (*AnalysisPublishExecutor) Kind() model.StepKind { return model.StepKindAnalysisPublish }

type analysisPublishConfig struct {
	RecordsPath string `json:"records_path"`
}

func (*AnalysisPublishExecutor) ValidateDefinition(_ context.Context, step model.StepDefinition) error {
	if len(step.Inputs) != 2 || strings.TrimSpace(step.Inputs["analysis"]) == "" ||
		strings.TrimSpace(step.Inputs["target"]) == "" {
		return fmt.Errorf("data.analysis_publish 必须且只能声明 analysis 与 target 输入")
	}
	if len(step.Outputs) != 1 || step.Outputs["batch"] != model.ResourceDatasetBatch {
		return fmt.Errorf("data.analysis_publish 必须且只能输出 batch: dataset_batch")
	}
	_, err := decodeAnalysisPublishConfig(step.Config)
	return err
}

func (e *AnalysisPublishExecutor) Execute(ctx context.Context, run orchestrator.StepRunContext) (orchestrator.StepResult, error) {
	config, err := decodeAnalysisPublishConfig(run.Config)
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	analysisRef, targetRef := run.Inputs["analysis"], run.Inputs["target"]
	if analysisRef.ResourceType != model.ResourceAnalysisResult || targetRef.ResourceType != model.ResourceDatasetBoundary {
		return orchestrator.StepResult{}, fmt.Errorf("data.analysis_publish 输入类型必须是 analysis_result + dataset_boundary")
	}
	var target model.DatasetBoundary
	if err := json.Unmarshal(targetRef.Boundary, &target); err != nil || target.DatasetID != targetRef.ResourceID {
		return orchestrator.StepResult{}, fmt.Errorf("target DatasetBoundary 非法")
	}
	result, err := e.analysis.GetResult(ctx, analysisRef.ResourceID)
	if err != nil || result.Status != model.AnalysisResultSucceeded {
		return orchestrator.StepResult{}, fmt.Errorf("AnalysisResult 不存在或未成功")
	}
	var output any
	if err := json.Unmarshal(result.Output, &output); err != nil {
		return orchestrator.StepResult{}, err
	}
	selected, err := jsonPath(output, config.RecordsPath)
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	records, ok := selected.([]any)
	if !ok || len(records) == 0 || len(records) > 10000 {
		return orchestrator.StepResult{}, fmt.Errorf("records_path 必须指向 1..10000 条记录数组")
	}
	inputs := make([]pipeline.BatchItemInput, len(records))
	for i, rawRecord := range records {
		record, ok := rawRecord.(map[string]any)
		if !ok {
			return orchestrator.StepResult{}, fmt.Errorf("第 %d 条分析记录必须是 object", i+1)
		}
		fields := record
		var provenance model.ItemProvenance
		if wrapped, exists := record["fields"]; exists {
			var fieldsOK bool
			fields, fieldsOK = wrapped.(map[string]any)
			if !fieldsOK {
				return orchestrator.StepResult{}, fmt.Errorf("第 %d 条 fields 必须是 object", i+1)
			}
			if rawProvenance, exists := record["provenance"]; exists {
				encoded, _ := json.Marshal(rawProvenance)
				if err := json.Unmarshal(encoded, &provenance); err != nil {
					return orchestrator.StepResult{}, fmt.Errorf("第 %d 条 provenance 非法: %w", i+1, err)
				}
			}
		}
		provenance.Model = result.Model
		provenance.QualityStatus = "agent_analyzed"
		inputs[i] = pipeline.BatchItemInput{Fields: fields, Provenance: provenance}
	}
	batch, err := e.datasets.GetOrCreateBatch(ctx, pipeline.CreateBatchInput{DatasetID: target.DatasetID,
		SourceTaskID: run.TaskID, SourceStepRunID: run.StepRunID}, run.Attempt)
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	batch, err = e.datasets.CommitBatchForStep(ctx, batch.ID, run.StepRunID, run.Attempt, inputs)
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	boundary, _ := json.Marshal(model.DatasetBatchBoundary{DatasetID: batch.DatasetID,
		FromSeq: batch.FromSeq, ToSeq: batch.ToSeq})
	return orchestrator.StepResult{Outputs: map[string]model.ResourceRef{"batch": {
		ResourceType: model.ResourceDatasetBatch, ResourceID: batch.ID, Boundary: boundary,
	}}, Metrics: map[string]any{"items": batch.ItemCount, "from_seq": batch.FromSeq, "to_seq": batch.ToSeq}}, nil
}

func (e *AnalysisPublishExecutor) Resume(ctx context.Context, run orchestrator.StepRunContext,
	_ json.RawMessage) (orchestrator.StepResult, error) {
	return e.Execute(ctx, run)
}

func decodeAnalysisPublishConfig(raw json.RawMessage) (analysisPublishConfig, error) {
	var config analysisPublishConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("data.analysis_publish config 非法: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return config, fmt.Errorf("data.analysis_publish config 只能包含一个 JSON object")
	}
	config.RecordsPath = strings.TrimSpace(config.RecordsPath)
	if config.RecordsPath == "" {
		return config, fmt.Errorf("records_path 不能为空")
	}
	return config, nil
}
