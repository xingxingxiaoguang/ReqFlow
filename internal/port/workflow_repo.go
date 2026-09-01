package port

import (
	"context"
	"encoding/json"
	"errors"

	"reqflow/internal/domain/workflow"
)

var (
	ErrWorkflowNotFound  = errors.New("workflow not found")
	ErrRevisionConflict  = errors.New("workflow draft revision conflict")
	ErrCommandIDConflict = errors.New("workflow command id is invalid")
)

type WorkflowDraftSummary struct {
	ID               string
	WorkspaceID      string
	Key              string
	Name             string
	Revision         int64
	ActiveRevisionID string
}

type DraftCommand struct {
	CommandID        string
	ExpectedRevision int64
	Type             string
	Payload          json.RawMessage
	ActorType        string
	ActorID          string
}

type DraftCommandResult struct {
	Draft  workflow.WorkflowDraft
	Issues []workflow.ValidationIssue
}

type WorkflowDraftRepo interface {
	CreateDraft(ctx context.Context, draft workflow.WorkflowDraft) error
	GetDraft(ctx context.Context, id string) (*workflow.WorkflowDraft, error)
	ListDrafts(ctx context.Context, workspaceID string, limit int) ([]WorkflowDraftSummary, error)
	ApplyCommand(ctx context.Context, workflowID string, command DraftCommand,
		mutate func(workflow.WorkflowDraft) (DraftCommandResult, error)) (DraftCommandResult, error)
}
