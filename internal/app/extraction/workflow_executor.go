package extraction

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type WorkflowDocumentExtractExecutor struct {
	service *Service
}

func NewWorkflowDocumentExtractExecutor(service *Service) (*WorkflowDocumentExtractExecutor, error) {
	if service == nil {
		return nil, fmt.Errorf("workflow document.extract: extraction service is required")
	}
	return &WorkflowDocumentExtractExecutor{service: service}, nil
}

func (*WorkflowDocumentExtractExecutor) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "document.extract", Version: 1}
}

func (e *WorkflowDocumentExtractExecutor) Execute(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	return e.run(ctx, execution)
}

func (e *WorkflowDocumentExtractExecutor) Resume(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	return e.run(ctx, execution)
}

func (e *WorkflowDocumentExtractExecutor) run(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	input, ok := extractionWorkflowInput(execution.Inputs, "documents")
	if !ok || input.ResourceType != domain.ResourceParsedDocuments || strings.TrimSpace(input.ResourceID) == "" {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("document.extract documents 输入必须是具体 ParsedDocumentSet")
	}
	if execution.Rules.DataContract == nil || execution.Rules.Extraction == nil {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("document.extract 缺少内联 DataContract 或 ExtractionSpec")
	}
	manifest, err := e.service.Extract(ctx, ExtractInput{ParsedDocumentSetID: input.ResourceID,
		DataContract: *execution.Rules.DataContract, ExtractionSpec: *execution.Rules.Extraction,
		ProducerNodeRunID: execution.NodeRunID, ProducerAttempt: execution.Attempt,
		Checkpoint: execution.Checkpoint, SaveCheckpoint: func(raw json.RawMessage) error {
			return execution.CheckpointWriter.Save(ctx, raw)
		}}, func(progress Progress) error {
		raw, marshalErr := json.Marshal(map[string]any{"phase": "extracting", "manifest_id": progress.RecordDraftSetID,
			"unit_key": progress.UnitKey, "ordinal": progress.Ordinal, "total": progress.Total,
			"completed": progress.Completed, "succeeded": progress.Succeeded, "failed": progress.Failed,
			"draft_count": progress.DraftCount, "status": progress.Status})
		if marshalErr != nil {
			return marshalErr
		}
		return execution.Progress.Report(ctx, raw)
	})
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	boundary, err := json.Marshal(map[string]any{"parsed_document_set_id": manifest.ParsedDocumentSetID,
		"data_contract_hash": manifest.DataContractHash, "extraction_spec_hash": manifest.ExtractionSpecHash,
		"schema_hash": manifest.SchemaHash, "model": manifest.Model})
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	return port.WorkflowCapabilityResult{Outputs: []domain.NodeResourceBinding{{Port: "drafts",
		ResourceType: domain.ResourceRecordDrafts, ResourceID: manifest.ID, Boundary: boundary}},
		Metrics: map[string]any{"status": manifest.Status, "units": manifest.UnitCount,
			"drafts": manifest.DraftCount, "failed_units": manifest.FailedUnitCount}}, nil
}

func extractionWorkflowInput(inputs []domain.NodeResourceBinding, portName string) (domain.NodeResourceBinding, bool) {
	for _, input := range inputs {
		if input.Direction == domain.BindingInput && input.Port == portName {
			return input, true
		}
	}
	return domain.NodeResourceBinding{}, false
}
