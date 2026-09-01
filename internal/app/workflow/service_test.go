package workflow

import (
	"context"
	"encoding/json"
	"testing"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type memoryWorkflowRepo struct {
	draft domain.WorkflowDraft
}

func (r *memoryWorkflowRepo) CreateDraft(_ context.Context, draft domain.WorkflowDraft) error {
	r.draft = draft
	return nil
}

func (r *memoryWorkflowRepo) GetDraft(_ context.Context, id string) (*domain.WorkflowDraft, error) {
	if r.draft.ID != id {
		return nil, port.ErrWorkflowNotFound
	}
	draft := r.draft
	return &draft, nil
}

func (r *memoryWorkflowRepo) ListDrafts(_ context.Context, _ string, _ int) ([]port.WorkflowDraftSummary, error) {
	return []port.WorkflowDraftSummary{{ID: r.draft.ID, WorkspaceID: r.draft.WorkspaceID,
		Key: r.draft.Key, Name: r.draft.Name, Revision: r.draft.Revision}}, nil
}

func (r *memoryWorkflowRepo) ApplyCommand(_ context.Context, workflowID string, command port.DraftCommand,
	mutate func(domain.WorkflowDraft) (port.DraftCommandResult, error)) (port.DraftCommandResult, error) {
	if workflowID != r.draft.ID {
		return port.DraftCommandResult{}, port.ErrWorkflowNotFound
	}
	if command.ExpectedRevision != r.draft.Revision {
		return port.DraftCommandResult{}, port.ErrRevisionConflict
	}
	result, err := mutate(r.draft)
	if err != nil {
		return port.DraftCommandResult{}, err
	}
	r.draft = result.Draft
	return result, nil
}

func TestDraftServiceDispatchesCommandThroughSingleEditor(t *testing.T) {
	catalog := editorCatalog(t)
	repo := &memoryWorkflowRepo{draft: editorDraft()}
	service, err := NewDraftService(repo, catalog)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ExecuteCommand(context.Background(), repo.draft.ID, CommandRequest{
		CommandID: "command-1", ExpectedRevision: repo.draft.Revision, Type: "set_node_config",
		Payload: json.RawMessage(`{"node_id":"source","config":{}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Draft.Revision != 2 || repo.draft.Revision != 2 {
		t.Fatalf("command revision = %d, repo revision = %d", result.Draft.Revision, repo.draft.Revision)
	}
	if _, err := service.ExecuteCommand(context.Background(), repo.draft.ID, CommandRequest{
		CommandID: "command-2", ExpectedRevision: 1, Type: "set_node_config",
		Payload: json.RawMessage(`{"node_id":"source","config":{}}`),
	}); err != port.ErrRevisionConflict {
		t.Fatalf("stale command error = %v, want revision conflict", err)
	}
}

func TestDraftServiceRejectsUnknownCommandWithoutRepositoryMutation(t *testing.T) {
	catalog := editorCatalog(t)
	repo := &memoryWorkflowRepo{draft: editorDraft()}
	service, _ := NewDraftService(repo, catalog)
	_, err := service.ExecuteCommand(context.Background(), repo.draft.ID, CommandRequest{
		CommandID: "command-unknown", ExpectedRevision: repo.draft.Revision, Type: "publish_now",
		Payload: json.RawMessage(`{}`),
	})
	if err == nil || repo.draft.Revision != 1 {
		t.Fatalf("未知命令必须失败且不修改草稿: err=%v revision=%d", err, repo.draft.Revision)
	}
}
