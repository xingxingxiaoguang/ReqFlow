package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type CreatePreviewRequest struct {
	DraftRevision int64           `json:"draft_revision"`
	Input         json.RawMessage `json:"input"`
}

type RunAcceptanceResponse struct {
	Draft   domain.WorkflowDraft    `json:"draft"`
	Preview *domain.WorkflowPreview `json:"preview"`
}

type PreviewService struct {
	repo    port.WorkflowDraftRepo
	catalog domain.CapabilityCatalog
	now     func() time.Time
}

func NewPreviewService(repo port.WorkflowDraftRepo, catalog domain.CapabilityCatalog) (*PreviewService, error) {
	if repo == nil || catalog == nil {
		return nil, fmt.Errorf("workflow preview service: repository and catalog are required")
	}
	return &PreviewService{repo: repo, catalog: catalog, now: time.Now}, nil
}

func (s *PreviewService) Create(ctx context.Context, workflowID string, request CreatePreviewRequest) (*domain.WorkflowPreview, error) {
	draft, err := s.repo.GetDraft(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	if request.DraftRevision != 0 && request.DraftRevision != draft.Revision {
		return nil, port.ErrRevisionConflict
	}
	if len(request.Input) == 0 || !json.Valid(request.Input) {
		return nil, fmt.Errorf("预览 input 必须是合法 JSON")
	}
	issues := domain.Validate(*draft, s.catalog, domain.ValidateDraft)
	if domain.HasErrors(issues) {
		return nil, domain.ValidationError{Issues: issues}
	}
	now := s.now()
	preview := &domain.WorkflowPreview{ID: uuid.NewString(), WorkflowID: workflowID, DraftRevision: draft.Revision,
		Status: domain.PreviewPassed, Input: append(json.RawMessage(nil), request.Input...),
		OutputManifest: s.manifest(*draft), Issues: issues, StartedBy: LocalActorID, StartedAt: now,
		FinishedAt: now, Temporary: true}
	if err := s.repo.CreatePreview(ctx, *preview); err != nil {
		return nil, err
	}
	return preview, nil
}

func (s *PreviewService) Get(ctx context.Context, previewID string) (*domain.WorkflowPreview, error) {
	return s.repo.GetPreview(ctx, previewID)
}

func (s *PreviewService) RunAcceptance(ctx context.Context, workflowID, caseID, previewID string) (*RunAcceptanceResponse, error) {
	draft, err := s.repo.GetDraft(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	preview, err := s.repo.GetPreview(ctx, previewID)
	if err != nil {
		return nil, err
	}
	if preview.WorkflowID != workflowID || preview.DraftRevision != draft.Revision || preview.Status != domain.PreviewPassed {
		return nil, fmt.Errorf("预览不是当前草稿版本的通过结果")
	}
	if !containsAcceptanceCase(*draft, caseID) {
		return nil, port.ErrAcceptanceNotFound
	}
	updated, err := s.repo.MarkAcceptancePassed(ctx, workflowID, caseID, draft.Revision, previewID, s.now())
	if err != nil {
		return nil, err
	}
	return &RunAcceptanceResponse{Draft: *updated, Preview: preview}, nil
}

func (s *PreviewService) manifest(draft domain.WorkflowDraft) json.RawMessage {
	manifest := make(map[string]map[string]map[string]any, len(draft.Nodes))
	for _, node := range draft.Nodes {
		definition, ok := s.catalog.Lookup(node.Capability)
		if !ok {
			continue
		}
		outputs := make(map[string]map[string]any)
		for _, output := range definition.Outputs {
			outputs[output.Name] = map[string]any{"node_id": node.ID, "port": output.Name,
				"resource_type": output.ResourceType, "temporary": true}
		}
		manifest[node.ID] = outputs
	}
	raw, _ := json.Marshal(manifest)
	return raw
}

func containsAcceptanceCase(draft domain.WorkflowDraft, caseID string) bool {
	for _, testCase := range draft.AcceptanceCases {
		if strings.TrimSpace(testCase.ID) == caseID {
			return true
		}
	}
	return false
}
