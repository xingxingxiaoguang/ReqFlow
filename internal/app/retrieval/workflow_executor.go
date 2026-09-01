package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type WorkflowBuildExecutor struct {
	service *Service
}

func NewWorkflowBuildExecutor(service *Service) (*WorkflowBuildExecutor, error) {
	if service == nil {
		return nil, fmt.Errorf("workflow retrieval.build: retrieval service is required")
	}
	return &WorkflowBuildExecutor{service: service}, nil
}

func (*WorkflowBuildExecutor) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "retrieval.build", Version: 1}
}

func (e *WorkflowBuildExecutor) Execute(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	return e.run(ctx, execution)
}

func (e *WorkflowBuildExecutor) Resume(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	return e.run(ctx, execution)
}

func (e *WorkflowBuildExecutor) run(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	input, ok := retrievalWorkflowInput(execution.Inputs, "dataset")
	if !ok || input.ResourceType != domain.ResourceDatasetBoundary || strings.TrimSpace(input.ResourceID) == "" {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("retrieval.build dataset 输入必须是具体 DatasetBoundary")
	}
	if execution.Rules.DataContract == nil || execution.Rules.Search == nil {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("retrieval.build 缺少内联 DataContract 或 SearchSpec")
	}
	var boundary struct {
		DatasetID  string `json:"dataset_id"`
		ThroughSeq int64  `json:"through_seq"`
	}
	if err := json.Unmarshal(input.Boundary, &boundary); err != nil {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("DatasetBoundary 非法: %w", err)
	}
	if boundary.DatasetID != "" && boundary.DatasetID != input.ResourceID {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("DatasetBoundary resource_id 不一致")
	}
	snapshot, err := e.service.BuildSnapshot(ctx, BuildInput{DatasetID: input.ResourceID,
		DataContract: *execution.Rules.DataContract, SearchSpec: *execution.Rules.Search,
		SourceSeq: boundary.ThroughSeq, ProducerNodeRunID: execution.NodeRunID,
		ProducerAttempt: execution.Attempt}, func(progress BuildProgress) error {
		raw, marshalErr := json.Marshal(map[string]any{"phase": "indexing", "snapshot_id": progress.SnapshotID,
			"processed_seq": progress.ProcessedSeq, "source_seq": progress.SourceSeq,
			"lexical_count": progress.LexicalCount, "vector_count": progress.VectorCount,
			"status": progress.Status})
		if marshalErr != nil {
			return marshalErr
		}
		return execution.Progress.Report(ctx, raw)
	})
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	outputBoundary, err := json.Marshal(map[string]any{"retrieval_snapshot_id": snapshot.ID,
		"source_seq": snapshot.SourceSeq, "data_contract_hash": snapshot.DataContractHash,
		"search_spec_hash": snapshot.SearchSpecHash, "embedding_model": snapshot.EmbeddingModel})
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	return port.WorkflowCapabilityResult{Outputs: []domain.NodeResourceBinding{{Port: "snapshot",
		ResourceType: domain.ResourceRetrievalSnapshot, ResourceID: snapshot.ID, Boundary: outputBoundary}},
		Metrics: map[string]any{"status": snapshot.Status, "source_seq": snapshot.SourceSeq,
			"lexical_count": snapshot.LexicalCount, "vector_count": snapshot.VectorCount}}, nil
}

func retrievalWorkflowInput(inputs []domain.NodeResourceBinding, portName string) (domain.NodeResourceBinding, bool) {
	for _, input := range inputs {
		if input.Direction == domain.BindingInput && input.Port == portName {
			return input, true
		}
	}
	return domain.NodeResourceBinding{}, false
}
