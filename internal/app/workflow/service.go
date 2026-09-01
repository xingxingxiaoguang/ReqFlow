package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

const (
	LocalWorkspaceID = "default"
	LocalActorType   = "user"
	LocalActorID     = "local-developer"
)

func NewDraftService(repo port.WorkflowDraftRepo, catalog domain.CapabilityCatalog) (*DraftService, error) {
	if repo == nil || catalog == nil {
		return nil, fmt.Errorf("workflow draft service: repository and catalog are required")
	}
	editor, err := NewDraftEditor(catalog)
	if err != nil {
		return nil, err
	}
	return &DraftService{repo: repo, catalog: catalog, editor: editor, now: time.Now}, nil
}

func ErrorStatus(err error) int {
	if errors.Is(err, port.ErrWorkflowNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, port.ErrRevisionConflict) || errors.Is(err, port.ErrCommandIDConflict) {
		return http.StatusConflict
	}
	return http.StatusUnprocessableEntity
}

func ErrorCode(err error) string {
	if errors.Is(err, port.ErrWorkflowNotFound) {
		return "not_found"
	}
	if errors.Is(err, port.ErrRevisionConflict) {
		return "revision_conflict"
	}
	if errors.Is(err, port.ErrCommandIDConflict) {
		return "command_id_conflict"
	}
	return "workflow_invalid"
}

func (s *DraftService) Capabilities() []domain.CapabilityDefinition {
	if catalog, ok := s.catalog.(interface {
		Definitions() []domain.CapabilityDefinition
	}); ok {
		return catalog.Definitions()
	}
	return nil
}

func (s *DraftService) Create(ctx context.Context, request CreateWorkflowRequest) (*WorkflowView, error) {
	now := s.now()
	workspaceID := strings.TrimSpace(request.WorkspaceID)
	if workspaceID == "" {
		workspaceID = LocalWorkspaceID
	}
	draft := domain.WorkflowDraft{
		ID: uuid.NewString(), WorkspaceID: workspaceID, Key: strings.TrimSpace(request.Key),
		Name: strings.TrimSpace(request.Name), Description: strings.TrimSpace(request.Description),
		Inputs:   append([]domain.WorkflowPort(nil), request.Inputs...),
		Outputs:  append([]domain.WorkflowPort(nil), request.Outputs...),
		Revision: 0, CreatedAt: now, UpdatedAt: now,
	}
	issues := domain.Validate(draft, s.catalog, domain.ValidateDraft)
	if domain.HasErrors(issues) {
		return nil, domain.ValidationError{Issues: issues}
	}
	if err := s.repo.CreateDraft(ctx, draft); err != nil {
		return nil, err
	}
	return &WorkflowView{Draft: draft, Issues: issues}, nil
}

func (s *DraftService) Get(ctx context.Context, id string) (*WorkflowView, error) {
	draft, err := s.repo.GetDraft(ctx, id)
	if err != nil {
		return nil, err
	}
	return &WorkflowView{Draft: *draft, Issues: domain.Validate(*draft, s.catalog, domain.ValidateDraft)}, nil
}

func (s *DraftService) List(ctx context.Context, workspaceID string, limit int) ([]WorkflowSummary, error) {
	if strings.TrimSpace(workspaceID) == "" {
		workspaceID = LocalWorkspaceID
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.repo.ListDrafts(ctx, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	result := make([]WorkflowSummary, 0, len(rows))
	for _, row := range rows {
		result = append(result, WorkflowSummary{ID: row.ID, WorkspaceID: row.WorkspaceID, Key: row.Key,
			Name: row.Name, Revision: row.Revision, ActiveRevisionID: row.ActiveRevisionID})
	}
	return result, nil
}

func (s *DraftService) ExecuteCommand(ctx context.Context, workflowID string, request CommandRequest) (*CommandResponse, error) {
	if strings.TrimSpace(request.CommandID) == "" || strings.TrimSpace(request.Type) == "" ||
		len(bytes.TrimSpace(request.Payload)) == 0 || !json.Valid(request.Payload) {
		return nil, fmt.Errorf("command_id、type 和合法 payload 必填")
	}
	command := port.DraftCommand{CommandID: request.CommandID, ExpectedRevision: request.ExpectedRevision,
		Type: request.Type, Payload: append(json.RawMessage(nil), request.Payload...),
		ActorType: LocalActorType, ActorID: LocalActorID}
	result, err := s.repo.ApplyCommand(ctx, workflowID, command, func(draft domain.WorkflowDraft) (port.DraftCommandResult, error) {
		next, err := s.apply(draft, request.Type, request.Payload)
		if err != nil {
			return port.DraftCommandResult{}, err
		}
		next.UpdatedAt = s.now()
		issues := domain.Validate(next, s.catalog, domain.ValidateDraft)
		if domain.HasErrors(issues) {
			return port.DraftCommandResult{}, domain.ValidationError{Issues: issues}
		}
		return port.DraftCommandResult{Draft: next, Issues: issues}, nil
	})
	if err != nil {
		return nil, err
	}
	return &CommandResponse{Draft: result.Draft, Issues: result.Issues}, nil
}

func (s *DraftService) Validate(ctx context.Context, workflowID string, request ValidateRequest) (*ValidateResponse, error) {
	draft, err := s.repo.GetDraft(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	mode := domain.ValidateDraft
	if request.Mode == string(domain.ValidatePublish) {
		mode = domain.ValidatePublish
	} else if request.Mode != "" && request.Mode != string(domain.ValidateDraft) {
		return nil, fmt.Errorf("mode 只能是 draft 或 publish")
	}
	issues := domain.Validate(*draft, s.catalog, mode)
	return &ValidateResponse{Draft: *draft, Issues: issues, Valid: !domain.HasErrors(issues)}, nil
}

func (s *DraftService) apply(draft domain.WorkflowDraft, commandType string, payload json.RawMessage) (domain.WorkflowDraft, error) {
	switch commandType {
	case "insert_between":
		var command InsertBetweenCommand
		return decodeAndApply(payload, &command, func(value InsertBetweenCommand) (domain.WorkflowDraft, error) {
			return s.editor.InsertBetween(draft, value)
		})
	case "append_after":
		var command AppendAfterCommand
		return decodeAndApply(payload, &command, func(value AppendAfterCommand) (domain.WorkflowDraft, error) {
			return s.editor.AppendAfter(draft, value)
		})
	case "prepend_before":
		var command PrependBeforeCommand
		return decodeAndApply(payload, &command, func(value PrependBeforeCommand) (domain.WorkflowDraft, error) {
			return s.editor.PrependBefore(draft, value)
		})
	case "remove_and_bridge":
		var command RemoveAndBridgeCommand
		return decodeAndApply(payload, &command, func(value RemoveAndBridgeCommand) (domain.WorkflowDraft, error) {
			return s.editor.RemoveAndBridge(draft, value)
		})
	case "replace_node":
		var command ReplaceNodeCommand
		return decodeAndApply(payload, &command, func(value ReplaceNodeCommand) (domain.WorkflowDraft, error) {
			return s.editor.ReplaceNode(draft, value)
		})
	case "set_node_config":
		var command SetNodeConfigCommand
		return decodeAndApply(payload, &command, func(value SetNodeConfigCommand) (domain.WorkflowDraft, error) {
			return s.editor.SetNodeConfig(draft, value)
		})
	case "bind_side_input":
		var command BindSideInputCommand
		return decodeAndApply(payload, &command, func(value BindSideInputCommand) (domain.WorkflowDraft, error) {
			return s.editor.BindSideInput(draft, value)
		})
	case "set_workflow_port":
		var command SetWorkflowPortCommand
		return decodeAndApply(payload, &command, func(value SetWorkflowPortCommand) (domain.WorkflowDraft, error) {
			return s.editor.SetWorkflowPort(draft, value)
		})
	case "set_data_contract":
		var value domain.DataContract
		return decodeAndApply(payload, &value, func(value domain.DataContract) (domain.WorkflowDraft, error) {
			return s.editor.SetDataContract(draft, value)
		})
	case "set_extraction_spec":
		var value domain.ExtractionSpec
		return decodeAndApply(payload, &value, func(value domain.ExtractionSpec) (domain.WorkflowDraft, error) {
			return s.editor.SetExtractionSpec(draft, value)
		})
	case "set_search_spec":
		var value domain.SearchSpec
		return decodeAndApply(payload, &value, func(value domain.SearchSpec) (domain.WorkflowDraft, error) {
			return s.editor.SetSearchSpec(draft, value)
		})
	case "set_output_contract":
		var value domain.OutputContract
		return decodeAndApply(payload, &value, func(value domain.OutputContract) (domain.WorkflowDraft, error) {
			return s.editor.SetOutputContract(draft, value)
		})
	case "confirm_decision":
		var command ConfirmDecisionCommand
		if err := decodeStrict(payload, &command); err != nil {
			return draft, err
		}
		command.ActorID, command.Confirmed = LocalActorID, s.now()
		return s.editor.ConfirmDecision(draft, command)
	case "upsert_acceptance_case":
		var value domain.AcceptanceCase
		return decodeAndApply(payload, &value, func(value domain.AcceptanceCase) (domain.WorkflowDraft, error) {
			return s.editor.UpsertAcceptanceCase(draft, value)
		})
	default:
		return draft, fmt.Errorf("不支持的 Workflow Draft Command %q", commandType)
	}
}

func decodeAndApply[T any](raw json.RawMessage, target *T, run func(T) (domain.WorkflowDraft, error)) (domain.WorkflowDraft, error) {
	if err := decodeStrict(raw, target); err != nil {
		return domain.WorkflowDraft{}, err
	}
	return run(*target)
}

func decodeStrict(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("命令 payload 非法: %w", err)
	}
	return nil
}
