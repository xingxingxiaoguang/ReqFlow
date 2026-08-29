package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reqflow/internal/app/orchestrator"
	"reqflow/internal/domain/model"
)

type DataTransformExecutor struct {
	cleaning *CleaningService
}

func NewDataTransformExecutor(cleaning *CleaningService) (*DataTransformExecutor, error) {
	if cleaning == nil {
		return nil, fmt.Errorf("data.transform: cleaning service is required")
	}
	return &DataTransformExecutor{cleaning: cleaning}, nil
}

func (*DataTransformExecutor) Kind() model.StepKind { return model.StepKindDataTransform }

func (*DataTransformExecutor) ValidateDefinition(_ context.Context, step model.StepDefinition) error {
	if len(step.Inputs) != 1 || strings.TrimSpace(step.Inputs["drafts"]) == "" {
		return fmt.Errorf("data.transform 必须且只能声明 drafts 输入")
	}
	if len(step.Outputs) != 1 || step.Outputs["records"] != model.ResourceTransformedRecords {
		return fmt.Errorf("data.transform 必须且只能声明 records: transformed_records 输出")
	}
	return validateEmptyExecutorConfig("data.transform", step.Config)
}

func (e *DataTransformExecutor) Execute(ctx context.Context, run orchestrator.StepRunContext) (orchestrator.StepResult, error) {
	input, ok := run.Inputs["drafts"]
	if !ok || input.ResourceType != model.ResourceRecordDrafts || strings.TrimSpace(input.ResourceID) == "" {
		return orchestrator.StepResult{}, fmt.Errorf("data.transform drafts 输入必须是具体 RecordDraftSet")
	}
	manifest, err := e.cleaning.Transform(ctx, TransformInput{RecordDraftSetID: input.ResourceID,
		SourceStepRunID: run.StepRunID, ProducerAttempt: run.Attempt}, cleaningProgressReporter(ctx, run, "transforming"))
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	profile, err := e.cleaning.GetProfile(ctx, manifest.ExtractionProfileID)
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	boundary, _ := json.Marshal(model.TransformedRecordsBoundary{RecordDraftSetID: manifest.RecordDraftSetID,
		ExtractionProfileID: manifest.ExtractionProfileID, TargetSchemaID: manifest.TargetSchemaID,
		ProfileHash: profile.ProfileHash, TransformEngineVersion: manifest.EngineVersion})
	return orchestrator.StepResult{Outputs: map[string]model.ResourceRef{
		"records": {ResourceType: model.ResourceTransformedRecords, ResourceID: manifest.ID, Boundary: boundary},
	}, Metrics: map[string]any{"status": manifest.Status, "records": manifest.TransformedCount,
		"changed_records": manifest.ChangedRecordCount, "issues": manifest.IssueCount}}, nil
}

func (e *DataTransformExecutor) Resume(ctx context.Context, run orchestrator.StepRunContext, _ json.RawMessage) (orchestrator.StepResult, error) {
	return e.Execute(ctx, run)
}

type DataValidateExecutor struct {
	cleaning *CleaningService
}

func NewDataValidateExecutor(cleaning *CleaningService) (*DataValidateExecutor, error) {
	if cleaning == nil {
		return nil, fmt.Errorf("data.validate: cleaning service is required")
	}
	return &DataValidateExecutor{cleaning: cleaning}, nil
}

func (*DataValidateExecutor) Kind() model.StepKind { return model.StepKindDataValidate }

func (*DataValidateExecutor) ValidateDefinition(_ context.Context, step model.StepDefinition) error {
	if len(step.Inputs) != 2 || strings.TrimSpace(step.Inputs["records"]) == "" || strings.TrimSpace(step.Inputs["dataset"]) == "" {
		return fmt.Errorf("data.validate 必须且只能声明 records 和 dataset 输入")
	}
	if len(step.Outputs) != 1 || step.Outputs["validation"] != model.ResourceValidationResults {
		return fmt.Errorf("data.validate 必须且只能声明 validation: validation_results 输出")
	}
	return validateEmptyExecutorConfig("data.validate", step.Config)
}

func (e *DataValidateExecutor) Execute(ctx context.Context, run orchestrator.StepRunContext) (orchestrator.StepResult, error) {
	records, recordsOK := run.Inputs["records"]
	dataset, datasetOK := run.Inputs["dataset"]
	if !recordsOK || records.ResourceType != model.ResourceTransformedRecords || strings.TrimSpace(records.ResourceID) == "" {
		return orchestrator.StepResult{}, fmt.Errorf("data.validate records 输入必须是具体 TransformedRecordSet")
	}
	if !datasetOK || dataset.ResourceType != model.ResourceDataset || strings.TrimSpace(dataset.ResourceID) == "" {
		return orchestrator.StepResult{}, fmt.Errorf("data.validate dataset 输入必须是可追加 Dataset")
	}
	manifest, err := e.cleaning.Validate(ctx, ValidateInput{TransformedRecordSetID: records.ResourceID,
		TargetDatasetID: dataset.ResourceID, SourceStepRunID: run.StepRunID,
		ProducerAttempt: run.Attempt}, cleaningProgressReporter(ctx, run, "validating"))
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	boundary, _ := json.Marshal(model.ValidationResultsBoundary{TransformedRecordSetID: manifest.TransformedRecordSetID,
		TargetDatasetID: manifest.TargetDatasetID, TargetSchemaID: manifest.TargetSchemaID,
		ValidatedThroughSeq: manifest.ValidatedThroughSeq, ValidationEngineVersion: manifest.EngineVersion})
	return orchestrator.StepResult{Outputs: map[string]model.ResourceRef{
		"validation": {ResourceType: model.ResourceValidationResults, ResourceID: manifest.ID, Boundary: boundary},
	}, Metrics: map[string]any{"status": manifest.Status, "records": manifest.RecordCount,
		"valid": manifest.ValidCount, "warning": manifest.WarningCount, "invalid": manifest.InvalidCount,
		"duplicate": manifest.DuplicateCount, "conflict": manifest.ConflictCount}}, nil
}

func (e *DataValidateExecutor) Resume(ctx context.Context, run orchestrator.StepRunContext, _ json.RawMessage) (orchestrator.StepResult, error) {
	return e.Execute(ctx, run)
}

func cleaningProgressReporter(ctx context.Context, run orchestrator.StepRunContext, phase string) func(CleaningProgress) error {
	return func(progress CleaningProgress) error {
		checkpoint, err := json.Marshal(map[string]any{"manifest_id": progress.ManifestID,
			"completed": progress.Completed, "total": progress.Total})
		if err != nil {
			return err
		}
		if err := run.Checkpoint.Save(ctx, checkpoint); err != nil {
			return err
		}
		event, err := json.Marshal(map[string]any{"phase": phase, "ordinal": progress.Ordinal,
			"total": progress.Total, "completed": progress.Completed,
			"reused": progress.Reused, "status": progress.Status})
		if err != nil {
			return err
		}
		return run.Progress.Report(ctx, event)
	}
}

func validateEmptyExecutorConfig(kind string, raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("{}")) {
		return nil
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil || len(value) != 0 {
		return fmt.Errorf("%s config 当前必须是空 object", kind)
	}
	return nil
}
