package workflow

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type memoryWorkflowRepo struct {
	draft    domain.WorkflowDraft
	preview  *domain.WorkflowPreview
	revision *domain.WorkflowRevision
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

func (r *memoryWorkflowRepo) CreatePreview(_ context.Context, preview domain.WorkflowPreview) error {
	r.preview = &preview
	return nil
}
func (r *memoryWorkflowRepo) GetPreview(_ context.Context, id string) (*domain.WorkflowPreview, error) {
	if r.preview == nil || r.preview.ID != id {
		return nil, port.ErrPreviewNotFound
	}
	preview := *r.preview
	return &preview, nil
}
func (r *memoryWorkflowRepo) MarkAcceptancePassed(_ context.Context, workflowID, caseID string, revision int64, previewID string, runAt time.Time) (*domain.WorkflowDraft, error) {
	if workflowID != r.draft.ID || revision != r.draft.Revision || r.preview == nil || previewID != r.preview.ID {
		return nil, port.ErrRevisionConflict
	}
	for index := range r.draft.AcceptanceCases {
		if r.draft.AcceptanceCases[index].ID != caseID {
			continue
		}
		r.draft.AcceptanceCases[index].LastPassed = true
		r.draft.AcceptanceCases[index].LastPassedRevision = revision
		r.draft.AcceptanceCases[index].LastPreviewID = previewID
		r.draft.AcceptanceCases[index].LastRunAt = runAt
		result := r.draft
		return &result, nil
	}
	return nil, port.ErrAcceptanceNotFound
}
func (r *memoryWorkflowRepo) PublishRevision(_ context.Context, _ string, _ int64, revision domain.WorkflowRevision) (*domain.WorkflowRevision, error) {
	revision.RevisionNo = 1
	r.revision = &revision
	return &revision, nil
}
func (r *memoryWorkflowRepo) ListRevisions(_ context.Context, _ string) ([]domain.WorkflowRevision, error) {
	return nil, nil
}
func (r *memoryWorkflowRepo) GetRevision(_ context.Context, _ string) (*domain.WorkflowRevision, error) {
	return nil, port.ErrRevisionNotFound
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

func TestPreviewAcceptanceAndPublicationStayOnDraftRevision(t *testing.T) {
	catalog := editorCatalog(t)
	repo := &memoryWorkflowRepo{draft: editorDraft()}
	service, _ := NewDraftService(repo, catalog)
	previewService, err := NewPreviewService(repo, catalog)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := NewPublicationService(repo, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExecuteCommand(context.Background(), repo.draft.ID, CommandRequest{CommandID: "case-command", ExpectedRevision: 1,
		Type: "upsert_acceptance_case", Payload: json.RawMessage(`{"id":"case_one","name":"样本","input":{},"expectation":{}}`)}); err != nil {
		t.Fatal(err)
	}
	preview, err := previewService.Create(context.Background(), repo.draft.ID, CreatePreviewRequest{DraftRevision: 2, Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	acceptance, err := previewService.RunAcceptance(context.Background(), repo.draft.ID, "case_one", preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	passed := acceptance.Draft.AcceptanceCases[0]
	if !passed.LastPassed || passed.LastPassedRevision != 2 || passed.LastPreviewID != preview.ID {
		t.Fatalf("验收结果没有绑定当前 draft revision: %+v", passed)
	}
	revision, err := publication.Publish(context.Background(), repo.draft.ID, PublishRequest{ExpectedRevision: 2})
	if err != nil {
		t.Fatal(err)
	}
	if revision.RevisionNo != 1 || revision.ContentHash == "" || repo.revision == nil {
		t.Fatalf("发布结果非法: %+v", revision)
	}
}
