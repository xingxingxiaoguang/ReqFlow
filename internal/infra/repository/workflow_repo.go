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
