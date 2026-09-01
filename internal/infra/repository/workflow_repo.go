package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type workflowRow struct {
	ID               string    `gorm:"column:id;primaryKey"`
	WorkspaceID      string    `gorm:"column:workspace_id"`
	Key              string    `gorm:"column:key"`
	Name             string    `gorm:"column:name"`
	Description      string    `gorm:"column:description"`
	DraftRevision    int64     `gorm:"column:draft_revision"`
	DraftDocument    string    `gorm:"column:draft_document;type:jsonb"`
	ActiveRevisionID *string   `gorm:"column:active_revision_id"`
	CreatedBy        string    `gorm:"column:created_by"`
	UpdatedBy        string    `gorm:"column:updated_by"`
	CreatedAt        time.Time `gorm:"column:created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at"`
}

func (workflowRow) TableName() string { return "workflows" }

type workflowCommandEventRow struct {
	ID             string    `gorm:"column:id;primaryKey"`
	CommandID      string    `gorm:"column:command_id"`
	WorkflowID     string    `gorm:"column:workflow_id"`
	BaseRevision   int64     `gorm:"column:base_revision"`
	ResultRevision int64     `gorm:"column:result_revision"`
	ActorType      string    `gorm:"column:actor_type"`
	ActorID        string    `gorm:"column:actor_id"`
	CommandType    string    `gorm:"column:command_type"`
	CommandPayload string    `gorm:"column:command_payload;type:jsonb"`
	ResultDocument string    `gorm:"column:result_document;type:jsonb"`
	CreatedAt      time.Time `gorm:"column:created_at"`
}

type workflowRevisionRow struct {
	ID          string    `gorm:"column:id;primaryKey"`
	WorkflowID  string    `gorm:"column:workflow_id"`
	RevisionNo  int64     `gorm:"column:revision_no"`
	Content     string    `gorm:"column:content;type:jsonb"`
	ContentHash string    `gorm:"column:content_hash"`
	PublishedBy string    `gorm:"column:published_by"`
	PublishedAt time.Time `gorm:"column:published_at"`
}

func (workflowRevisionRow) TableName() string { return "workflow_revisions" }

type workflowPreviewRow struct {
	ID             string    `gorm:"column:id;primaryKey"`
	WorkflowID     string    `gorm:"column:workflow_id"`
	DraftRevision  int64     `gorm:"column:draft_revision"`
	Status         string    `gorm:"column:status"`
	InputManifest  string    `gorm:"column:input_manifest;type:jsonb"`
	OutputManifest string    `gorm:"column:output_manifest;type:jsonb"`
	Issues         string    `gorm:"column:issues;type:jsonb"`
	StartedBy      string    `gorm:"column:started_by"`
	StartedAt      time.Time `gorm:"column:started_at"`
	FinishedAt     time.Time `gorm:"column:finished_at"`
	Temporary      bool      `gorm:"column:temporary"`
}

func (workflowPreviewRow) TableName() string { return "workflow_previews" }

func (workflowCommandEventRow) TableName() string { return "workflow_command_events" }

type WorkflowRepo struct{ db *gorm.DB }

func NewWorkflowRepo(db *gorm.DB) *WorkflowRepo { return &WorkflowRepo{db: db} }

func (r *WorkflowRepo) CreateDraft(ctx context.Context, draft domain.WorkflowDraft) error {
	if r == nil || r.db == nil {
		return errors.New("workflow repository is not initialized")
	}
	if strings.TrimSpace(draft.ID) == "" {
		draft.ID = uuid.NewString()
	}
	document, err := json.Marshal(draft)
	if err != nil {
		return fmt.Errorf("serialize workflow draft: %w", err)
	}
	row := workflowRow{ID: draft.ID, WorkspaceID: draft.WorkspaceID, Key: draft.Key, Name: draft.Name,
		Description: draft.Description, DraftRevision: draft.Revision, DraftDocument: string(document),
		CreatedBy: "local-developer", UpdatedBy: "local-developer", CreatedAt: draft.CreatedAt, UpdatedAt: draft.UpdatedAt}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now()
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = row.CreatedAt
	}
	return r.db.WithContext(ctx).Create(&row).Error
}

func (r *WorkflowRepo) GetDraft(ctx context.Context, id string) (*domain.WorkflowDraft, error) {
	var row workflowRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, port.ErrWorkflowNotFound
	} else if err != nil {
		return nil, err
	}
	var draft domain.WorkflowDraft
	if err := json.Unmarshal([]byte(row.DraftDocument), &draft); err != nil {
		return nil, fmt.Errorf("decode workflow draft: %w", err)
	}
	return &draft, nil
}

func (r *WorkflowRepo) ListDrafts(ctx context.Context, workspaceID string, limit int) ([]port.WorkflowDraftSummary, error) {
	var rows []workflowRow
	if err := r.db.WithContext(ctx).Where("workspace_id = ?", workspaceID).
		Order("updated_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]port.WorkflowDraftSummary, 0, len(rows))
	for _, row := range rows {
		result = append(result, port.WorkflowDraftSummary{ID: row.ID, WorkspaceID: row.WorkspaceID, Key: row.Key,
			Name: row.Name, Revision: row.DraftRevision, ActiveRevisionID: stringValue(row.ActiveRevisionID)})
	}
	return result, nil
}

func (r *WorkflowRepo) ApplyCommand(ctx context.Context, workflowID string, command port.DraftCommand,
	mutate func(domain.WorkflowDraft) (port.DraftCommandResult, error)) (port.DraftCommandResult, error) {
	var result port.DraftCommandResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var event workflowCommandEventRow
		if err := tx.Where("command_id = ?", command.CommandID).First(&event).Error; err == nil {
			if event.WorkflowID != workflowID {
				return port.ErrCommandIDConflict
			}
			if err := json.Unmarshal([]byte(event.ResultDocument), &result); err != nil {
				return fmt.Errorf("decode idempotent command result: %w", err)
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var row workflowRow
		if err := tx.Clauses(lockForUpdate()).Where("id = ?", workflowID).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return port.ErrWorkflowNotFound
		} else if err != nil {
			return err
		}
		if row.DraftRevision != command.ExpectedRevision {
			return port.ErrRevisionConflict
		}
		var draft domain.WorkflowDraft
		if err := json.Unmarshal([]byte(row.DraftDocument), &draft); err != nil {
			return fmt.Errorf("decode locked workflow draft: %w", err)
		}
		var err error
		result, err = mutate(draft)
		if err != nil {
			return err
		}
		result.Draft.Revision = command.ExpectedRevision + 1
		result.Draft.UpdatedAt = time.Now()
		document, err := json.Marshal(result.Draft)
		if err != nil {
			return fmt.Errorf("serialize workflow draft result: %w", err)
		}
		resultDocument, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("serialize workflow command result: %w", err)
		}
		if err := tx.Model(&workflowRow{}).Where("id = ? AND draft_revision = ?", workflowID, command.ExpectedRevision).
			Updates(map[string]any{"draft_revision": result.Draft.Revision, "draft_document": string(document),
				"key": result.Draft.Key, "name": result.Draft.Name, "description": result.Draft.Description,
				"updated_by": command.ActorID, "updated_at": result.Draft.UpdatedAt}).Error; err != nil {
			return err
		}
		return tx.Create(&workflowCommandEventRow{ID: uuid.NewString(), CommandID: command.CommandID, WorkflowID: workflowID,
			BaseRevision: command.ExpectedRevision, ResultRevision: result.Draft.Revision, ActorType: command.ActorType,
			ActorID: command.ActorID, CommandType: command.Type, CommandPayload: string(command.Payload),
			ResultDocument: string(resultDocument), CreatedAt: result.Draft.UpdatedAt}).Error
	})
	return result, err
}

func lockForUpdate() clause.Locking {
	return clause.Locking{Strength: "UPDATE"}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (r *WorkflowRepo) CreatePreview(ctx context.Context, preview domain.WorkflowPreview) error {
	input := jsonOrEmpty(preview.Input)
	output := jsonOrEmpty(preview.OutputManifest)
	issues, err := json.Marshal(preview.Issues)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Create(&workflowPreviewRow{ID: preview.ID, WorkflowID: preview.WorkflowID,
		DraftRevision: preview.DraftRevision, Status: string(preview.Status), InputManifest: string(input),
		OutputManifest: string(output), Issues: string(issues), StartedBy: preview.StartedBy,
		StartedAt: preview.StartedAt, FinishedAt: preview.FinishedAt, Temporary: preview.Temporary}).Error
}

func (r *WorkflowRepo) GetPreview(ctx context.Context, id string) (*domain.WorkflowPreview, error) {
	var row workflowPreviewRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, port.ErrPreviewNotFound
	} else if err != nil {
		return nil, err
	}
	var issues []domain.ValidationIssue
	if err := json.Unmarshal([]byte(row.Issues), &issues); err != nil {
		return nil, err
	}
	return &domain.WorkflowPreview{ID: row.ID, WorkflowID: row.WorkflowID, DraftRevision: row.DraftRevision,
		Status: domain.PreviewStatus(row.Status), Input: json.RawMessage(row.InputManifest),
		OutputManifest: json.RawMessage(row.OutputManifest), Issues: issues, StartedBy: row.StartedBy,
		StartedAt: row.StartedAt, FinishedAt: row.FinishedAt, Temporary: row.Temporary}, nil
}

func (r *WorkflowRepo) MarkAcceptancePassed(ctx context.Context, workflowID, caseID string,
	draftRevision int64, previewID string, runAt time.Time) (*domain.WorkflowDraft, error) {
	var result *domain.WorkflowDraft
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row workflowRow
		if err := tx.Clauses(lockForUpdate()).Where("id = ?", workflowID).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return port.ErrWorkflowNotFound
		} else if err != nil {
			return err
		}
		if row.DraftRevision != draftRevision {
			return port.ErrRevisionConflict
		}
		var draft domain.WorkflowDraft
		if err := json.Unmarshal([]byte(row.DraftDocument), &draft); err != nil {
			return err
		}
		found := false
		for index := range draft.AcceptanceCases {
			if draft.AcceptanceCases[index].ID != caseID {
				continue
			}
			draft.AcceptanceCases[index].LastPassed = true
			draft.AcceptanceCases[index].LastPassedRevision = draftRevision
			draft.AcceptanceCases[index].LastPreviewID = previewID
			draft.AcceptanceCases[index].LastRunAt = runAt
			found = true
			break
		}
		if !found {
			return port.ErrAcceptanceNotFound
		}
		document, err := json.Marshal(draft)
		if err != nil {
			return err
		}
		if err := tx.Model(&workflowRow{}).Where("id = ? AND draft_revision = ?", workflowID, draftRevision).
			Update("draft_document", string(document)).Error; err != nil {
			return err
		}
		result = &draft
		return nil
	})
	return result, err
}

func (r *WorkflowRepo) PublishRevision(ctx context.Context, workflowID string, expectedRevision int64,
	revision domain.WorkflowRevision) (*domain.WorkflowRevision, error) {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row workflowRow
		if err := tx.Clauses(lockForUpdate()).Where("id = ?", workflowID).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return port.ErrWorkflowNotFound
		} else if err != nil {
			return err
		}
		if row.DraftRevision != expectedRevision {
			return port.ErrRevisionConflict
		}
		var nextRevision int64
		if err := tx.Model(&workflowRevisionRow{}).Where("workflow_id = ?", workflowID).
			Select("COALESCE(MAX(revision_no), 0) + 1").Scan(&nextRevision).Error; err != nil {
			return err
		}
		revision.RevisionNo = nextRevision
		content, err := json.Marshal(revision)
		if err != nil {
			return err
		}
		if err := tx.Create(&workflowRevisionRow{ID: revision.ID, WorkflowID: workflowID, RevisionNo: nextRevision,
			Content: string(content), ContentHash: revision.ContentHash, PublishedBy: revision.PublishedBy,
			PublishedAt: revision.PublishedAt}).Error; err != nil {
			return err
		}
		return tx.Model(&workflowRow{}).Where("id = ? AND draft_revision = ?", workflowID, expectedRevision).
			Update("active_revision_id", revision.ID).Error
	})
	if err != nil {
		return nil, err
	}
	return &revision, nil
}

func (r *WorkflowRepo) ListRevisions(ctx context.Context, workflowID string) ([]domain.WorkflowRevision, error) {
	var rows []workflowRevisionRow
	if err := r.db.WithContext(ctx).Where("workflow_id = ?", workflowID).Order("revision_no DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]domain.WorkflowRevision, 0, len(rows))
	for _, row := range rows {
		var revision domain.WorkflowRevision
		if err := json.Unmarshal([]byte(row.Content), &revision); err != nil {
			return nil, err
		}
		result = append(result, revision)
	}
	return result, nil
}

func (r *WorkflowRepo) GetRevision(ctx context.Context, id string) (*domain.WorkflowRevision, error) {
	var row workflowRevisionRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, port.ErrRevisionNotFound
	} else if err != nil {
		return nil, err
	}
	var revision domain.WorkflowRevision
	if err := json.Unmarshal([]byte(row.Content), &revision); err != nil {
		return nil, err
	}
	return &revision, nil
}
