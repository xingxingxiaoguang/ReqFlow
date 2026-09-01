package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type WorkflowSourceParseExecutor struct {
	assets *AssetService
}

func NewWorkflowSourceParseExecutor(assets *AssetService) (*WorkflowSourceParseExecutor, error) {
	if assets == nil {
		return nil, fmt.Errorf("workflow source.parse: asset service is required")
	}
	return &WorkflowSourceParseExecutor{assets: assets}, nil
}

func (*WorkflowSourceParseExecutor) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "source.parse", Version: 1}
}

func (e *WorkflowSourceParseExecutor) Execute(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	return e.run(ctx, execution)
}

func (e *WorkflowSourceParseExecutor) Resume(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	return e.run(ctx, execution)
}

func (e *WorkflowSourceParseExecutor) run(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	input, ok := workflowInput(execution.Inputs, "assets")
	if !ok || input.ResourceType != domain.ResourceAssetSet || strings.TrimSpace(input.ResourceID) == "" {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("source.parse assets 输入必须是具体 AssetSet")
	}
	manifest, err := e.assets.ParseWorkflowAssetSet(ctx, WorkflowParseInput{ResourceID: input.ResourceID,
		ExecutionID: execution.NodeRunID, Attempt: execution.Attempt}, func(progress ParseAssetSetProgress) error {
		checkpoint, marshalErr := json.Marshal(map[string]any{"manifest_id": progress.ManifestID,
			"completed": progress.Completed, "succeeded": progress.Succeeded, "failed": progress.Failed})
		if marshalErr != nil {
			return marshalErr
		}
		if err := execution.CheckpointWriter.Save(ctx, checkpoint); err != nil {
			return err
		}
		event, marshalErr := json.Marshal(map[string]any{"phase": "parsing", "asset_id": progress.AssetID,
			"ordinal": progress.Ordinal, "total": progress.Total, "completed": progress.Completed,
			"succeeded": progress.Succeeded, "failed": progress.Failed, "status": progress.Status})
		if marshalErr != nil {
			return marshalErr
		}
		return execution.Progress.Report(ctx, event)
	})
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	boundary, err := json.Marshal(map[string]any{"asset_set_id": manifest.AssetSetID,
		"parser_name": manifest.ParserName, "parser_version": manifest.ParserVersion})
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	return port.WorkflowCapabilityResult{Outputs: []domain.NodeResourceBinding{{Port: "documents",
		ResourceType: domain.ResourceParsedDocuments, ResourceID: manifest.ID, Boundary: boundary}},
		Metrics: map[string]any{"status": manifest.Status, "total": manifest.TotalCount,
			"succeeded": manifest.SucceededCount, "failed": manifest.FailedCount}}, nil
}

type WorkflowDataTransformExecutor struct {
	cleaning *CleaningService
}

func NewWorkflowDataTransformExecutor(cleaning *CleaningService) (*WorkflowDataTransformExecutor, error) {
	if cleaning == nil {
		return nil, fmt.Errorf("workflow data.transform: cleaning service is required")
	}
	return &WorkflowDataTransformExecutor{cleaning: cleaning}, nil
}

func (*WorkflowDataTransformExecutor) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "data.transform", Version: 1}
}

func (e *WorkflowDataTransformExecutor) Execute(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	return e.run(ctx, execution)
}

func (e *WorkflowDataTransformExecutor) Resume(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	return e.run(ctx, execution)
}

func (e *WorkflowDataTransformExecutor) run(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	input, ok := workflowInput(execution.Inputs, "drafts")
	if !ok || input.ResourceType != domain.ResourceRecordDrafts || strings.TrimSpace(input.ResourceID) == "" {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("data.transform drafts 输入必须是具体 RecordDraftSet")
	}
	rules := json.RawMessage(`[]`)
	if execution.Rules.Extraction != nil {
		rules, _ = json.Marshal(execution.Rules.Extraction.NormalizationRules)
	}
	manifest, err := e.cleaning.TransformWorkflow(ctx, WorkflowTransformInput{ResourceID: input.ResourceID,
		ExecutionID: execution.NodeRunID, Attempt: execution.Attempt, NormalizationRules: rules},
		workflowCleaningProgress(ctx, execution, "transforming"))
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	profile, err := e.cleaning.GetProfile(ctx, manifest.ExtractionProfileID)
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	boundary, err := json.Marshal(map[string]any{"record_draft_set_id": manifest.RecordDraftSetID,
		"target_schema_id": manifest.TargetSchemaID, "profile_hash": profile.ProfileHash,
		"transform_engine_version": manifest.EngineVersion})
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	return port.WorkflowCapabilityResult{Outputs: []domain.NodeResourceBinding{{Port: "records",
		ResourceType: domain.ResourceTransformedRecords, ResourceID: manifest.ID, Boundary: boundary}},
		Metrics: map[string]any{"status": manifest.Status, "records": manifest.TransformedCount,
			"changed_records": manifest.ChangedRecordCount, "issues": manifest.IssueCount}}, nil
}

type WorkflowDataValidateExecutor struct {
	cleaning *CleaningService
}

type WorkflowDataPublishExecutor struct {
	publish *PublishService
}

func NewWorkflowDataPublishExecutor(publish *PublishService) (*WorkflowDataPublishExecutor, error) {
	if publish == nil {
		return nil, fmt.Errorf("workflow data.publish: publish service is required")
	}
	return &WorkflowDataPublishExecutor{publish: publish}, nil
}

func (*WorkflowDataPublishExecutor) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "data.publish", Version: 1}
}

func (e *WorkflowDataPublishExecutor) Execute(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	return e.run(ctx, execution)
}

func (e *WorkflowDataPublishExecutor) Resume(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	return e.run(ctx, execution)
}

func (e *WorkflowDataPublishExecutor) run(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	input, ok := workflowInput(execution.Inputs, "approved")
	if !ok || input.ResourceType != domain.ResourceApprovedRecords || strings.TrimSpace(input.ResourceID) == "" {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("data.publish approved 输入必须是具体 ApprovedRecordSet")
	}
	batch, err := e.publish.PublishWorkflowApproved(ctx, WorkflowPublishInput{ResourceID: input.ResourceID,
		RunID: execution.RunID, ExecutionID: execution.NodeRunID, Attempt: execution.Attempt})
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	progress, err := json.Marshal(map[string]any{"phase": "published", "dataset_id": batch.DatasetID,
		"batch_id": batch.ID, "item_count": batch.ItemCount, "from_seq": batch.FromSeq, "to_seq": batch.ToSeq})
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	if err := execution.Progress.Report(ctx, progress); err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	batchBoundary, err := json.Marshal(map[string]any{"dataset_id": batch.DatasetID,
		"from_seq": batch.FromSeq, "to_seq": batch.ToSeq})
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	datasetBoundary, err := json.Marshal(map[string]any{"dataset_id": batch.DatasetID, "through_seq": batch.ToSeq})
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	return port.WorkflowCapabilityResult{Outputs: []domain.NodeResourceBinding{
		{Port: "dataset", ResourceType: domain.ResourceDatasetBoundary, ResourceID: batch.DatasetID, Boundary: datasetBoundary},
		{Port: "batch", ResourceType: domain.ResourceDatasetBatch, ResourceID: batch.ID, Boundary: batchBoundary},
	}, Metrics: map[string]any{"status": batch.Status, "items": batch.ItemCount,
		"from_seq": batch.FromSeq, "to_seq": batch.ToSeq}}, nil
}

func NewWorkflowDataValidateExecutor(cleaning *CleaningService) (*WorkflowDataValidateExecutor, error) {
	if cleaning == nil {
		return nil, fmt.Errorf("workflow data.validate: cleaning service is required")
	}
	return &WorkflowDataValidateExecutor{cleaning: cleaning}, nil
}

func (*WorkflowDataValidateExecutor) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "data.validate", Version: 1}
}

func (e *WorkflowDataValidateExecutor) Execute(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	return e.run(ctx, execution)
}

func (e *WorkflowDataValidateExecutor) Resume(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	return e.run(ctx, execution)
}

func (e *WorkflowDataValidateExecutor) run(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	records, recordsOK := workflowInput(execution.Inputs, "records")
	dataset, datasetOK := workflowInput(execution.Inputs, "dataset")
	if !recordsOK || records.ResourceType != domain.ResourceTransformedRecords || strings.TrimSpace(records.ResourceID) == "" {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("data.validate records 输入必须是具体 TransformedRecordSet")
	}
	if !datasetOK || dataset.ResourceType != domain.ResourceDataset || strings.TrimSpace(dataset.ResourceID) == "" {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("data.validate dataset 输入必须是具体 Dataset")
	}
	rules := json.RawMessage(`[]`)
	if execution.Rules.Extraction != nil {
		rules, _ = json.Marshal(execution.Rules.Extraction.ValidationRules)
	}
	manifest, err := e.cleaning.ValidateWorkflow(ctx, WorkflowValidateInput{RecordsID: records.ResourceID,
		DatasetID: dataset.ResourceID, ExecutionID: execution.NodeRunID,
		Attempt: execution.Attempt, ValidationRules: rules}, workflowCleaningProgress(ctx, execution, "validating"))
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	boundary, err := json.Marshal(map[string]any{"transformed_record_set_id": manifest.TransformedRecordSetID,
		"target_dataset_id": manifest.TargetDatasetID, "target_schema_id": manifest.TargetSchemaID,
		"validated_through_seq": manifest.ValidatedThroughSeq, "validation_engine_version": manifest.EngineVersion})
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	return port.WorkflowCapabilityResult{Outputs: []domain.NodeResourceBinding{{Port: "validation",
		ResourceType: domain.ResourceValidationResults, ResourceID: manifest.ID, Boundary: boundary}},
		Metrics: map[string]any{"status": manifest.Status, "records": manifest.RecordCount,
			"valid": manifest.ValidCount, "warning": manifest.WarningCount, "invalid": manifest.InvalidCount,
			"duplicate": manifest.DuplicateCount, "conflict": manifest.ConflictCount}}, nil
}

func workflowInput(inputs []domain.NodeResourceBinding, portName string) (domain.NodeResourceBinding, bool) {
	for _, input := range inputs {
		if input.Port == portName && input.Direction == domain.BindingInput {
			return input, true
		}
	}
	return domain.NodeResourceBinding{}, false
}

func workflowCleaningProgress(ctx context.Context, execution port.WorkflowCapabilityExecution, phase string) func(CleaningProgress) error {
	return func(progress CleaningProgress) error {
		checkpoint, err := json.Marshal(map[string]any{"manifest_id": progress.ManifestID,
			"completed": progress.Completed, "total": progress.Total})
		if err != nil {
			return err
		}
		if err := execution.CheckpointWriter.Save(ctx, checkpoint); err != nil {
			return err
		}
		event, err := json.Marshal(map[string]any{"phase": phase, "ordinal": progress.Ordinal,
			"total": progress.Total, "completed": progress.Completed, "reused": progress.Reused,
			"status": progress.Status})
		if err != nil {
			return err
		}
		return execution.Progress.Report(ctx, event)
	}
}
