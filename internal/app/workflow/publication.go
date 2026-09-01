package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type PublishRequest struct {
	ExpectedRevision int64 `json:"expected_revision"`
}

type PublicationService struct {
	repo    port.WorkflowDraftRepo
	catalog domain.CapabilityCatalog
}

func NewPublicationService(repo port.WorkflowDraftRepo, catalog domain.CapabilityCatalog) (*PublicationService, error) {
	if repo == nil || catalog == nil {
		return nil, fmt.Errorf("workflow publication service: repository and catalog are required")
	}
	return &PublicationService{repo: repo, catalog: catalog}, nil
}

func (s *PublicationService) Publish(ctx context.Context, workflowID string, request PublishRequest) (*domain.WorkflowRevision, error) {
	draft, err := s.repo.GetDraft(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	if draft.Revision != request.ExpectedRevision {
		return nil, port.ErrRevisionConflict
	}
	revision, err := domain.BuildRevision(*draft, s.catalog, uuid.NewString(), LocalActorID, nowUTC())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(revision.WorkflowID) == "" {
		return nil, fmt.Errorf("workflow revision 缺少 workflow id")
	}
	return s.repo.PublishRevision(ctx, workflowID, request.ExpectedRevision, *revision)
}

func (s *PublicationService) ListRevisions(ctx context.Context, workflowID string) ([]domain.WorkflowRevision, error) {
	return s.repo.ListRevisions(ctx, workflowID)
}

func (s *PublicationService) GetRevision(ctx context.Context, revisionID string) (*domain.WorkflowRevision, error) {
	return s.repo.GetRevision(ctx, revisionID)
}

func nowUTC() time.Time { return time.Now().UTC() }
