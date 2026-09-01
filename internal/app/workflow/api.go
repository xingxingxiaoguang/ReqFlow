package workflow

import (
	"encoding/json"
	"time"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type CreateWorkflowRequest struct {
	WorkspaceID string                `json:"workspace_id"`
	Key         string                `json:"key"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Inputs      []domain.WorkflowPort `json:"inputs"`
	Outputs     []domain.WorkflowPort `json:"outputs"`
}

type WorkflowView struct {
	Draft          domain.WorkflowDraft     `json:"draft"`
	Issues         []domain.ValidationIssue `json:"issues"`
	ActiveRevision string                   `json:"active_revision_id,omitempty"`
}

type WorkflowSummary struct {
	ID               string `json:"id"`
	WorkspaceID      string `json:"workspace_id"`
	Key              string `json:"key"`
	Name             string `json:"name"`
	Revision         int64  `json:"revision"`
	ActiveRevisionID string `json:"active_revision_id,omitempty"`
}

type CommandRequest struct {
	CommandID        string          `json:"command_id"`
	ExpectedRevision int64           `json:"expected_revision"`
	Type             string          `json:"type"`
	Payload          json.RawMessage `json:"payload"`
}

type CommandResponse struct {
	Draft  domain.WorkflowDraft     `json:"draft"`
	Issues []domain.ValidationIssue `json:"issues"`
}

type ValidateRequest struct {
	Mode string `json:"mode"`
}

type ValidateResponse struct {
	Draft  domain.WorkflowDraft     `json:"draft"`
	Issues []domain.ValidationIssue `json:"issues"`
	Valid  bool                     `json:"valid"`
}

type DraftService struct {
	repo    port.WorkflowDraftRepo
	catalog domain.CapabilityCatalog
	editor  *DraftEditor
	now     func() time.Time
}
