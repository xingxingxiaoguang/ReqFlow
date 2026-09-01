package port

import (
	"context"
	"encoding/json"

	domain "reqflow/internal/domain/workflow"
)

type WorkflowCheckpointWriter interface {
	Save(ctx context.Context, checkpoint json.RawMessage) error
}

type WorkflowProgressReporter interface {
	Report(ctx context.Context, progress json.RawMessage) error
}

type WorkflowCapabilityExecution struct {
	WorkspaceID      string
	RunID            string
	NodeRunID        string
	Attempt          int
	Node             domain.ResolvedNode
	Rules            domain.RuleBundle
	Inputs           []domain.NodeResourceBinding
	Checkpoint       json.RawMessage
	CheckpointWriter WorkflowCheckpointWriter
	Progress         WorkflowProgressReporter
}

type WorkflowCapabilityResult struct {
	Outputs  []domain.NodeResourceBinding
	Status   domain.NodeRunStatus
	Code     string
	Message  string
	Question *domain.HumanQuestion
	Metrics  map[string]any
}

type WorkflowCapabilityExecutor interface {
	Capability() domain.CapabilityRef
	Execute(ctx context.Context, execution WorkflowCapabilityExecution) (WorkflowCapabilityResult, error)
	Resume(ctx context.Context, execution WorkflowCapabilityExecution) (WorkflowCapabilityResult, error)
}
