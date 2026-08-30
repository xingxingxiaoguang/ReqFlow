package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type AgentSessionRepo struct{ db *gorm.DB }

func NewAgentSessionRepo(db *gorm.DB) *AgentSessionRepo { return &AgentSessionRepo{db: db} }

type agentSessionRow struct {
	ID          string    `gorm:"column:id;primaryKey"`
	WorkspaceID string    `gorm:"column:workspace_id"`
	Title       string    `gorm:"column:title"`
	Status      string    `gorm:"column:status"`
	Context     string    `gorm:"column:context;type:jsonb"`
	LastError   string    `gorm:"column:last_error"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (agentSessionRow) TableName() string { return "agent_sessions" }

func (r *AgentSessionRepo) CreateAgentSession(ctx context.Context, session *model.AgentSession) error {
	if session.ID == "" {
		session.ID = uuid.NewString()
	}
	now := time.Now()
	if session.WorkspaceID == "" {
		session.WorkspaceID = "default"
	}
	if session.Status == "" {
		session.Status = model.AgentSessionIdle
	}
	if len(session.Context) == 0 {
		session.Context = json.RawMessage(`{"messages":[]}`)
	}
	session.CreatedAt, session.UpdatedAt = now, now
	return r.db.WithContext(ctx).Create(agentSessionToRow(session)).Error
}

func (r *AgentSessionRepo) ListAgentSessions(ctx context.Context, workspaceID string, limit int) ([]model.AgentSession, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var rows []agentSessionRow
	if err := r.db.WithContext(ctx).Where("workspace_id = ?", workspaceID).
		Order("updated_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.AgentSession, len(rows))
	for i := range rows {
		out[i] = agentSessionToModel(rows[i])
	}
	return out, nil
}

func (r *AgentSessionRepo) GetAgentSession(ctx context.Context, id string) (*model.AgentSession, error) {
	var row agentSessionRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	session := agentSessionToModel(row)
	return &session, nil
}

func (r *AgentSessionRepo) BeginAgentSession(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Model(&agentSessionRow{}).
		Where("id = ? AND status <> ?", id, model.AgentSessionRunning).
		Updates(map[string]any{"status": model.AgentSessionRunning, "last_error": "", "updated_at": time.Now()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return port.ErrAgentSessionRunning
	}
	return nil
}

func (r *AgentSessionRepo) SaveAgentSession(ctx context.Context, session *model.AgentSession) error {
	session.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Model(&agentSessionRow{}).Where("id = ?", session.ID).
		Updates(map[string]any{
			"title": session.Title, "status": session.Status, "context": string(session.Context),
			"last_error": session.LastError, "updated_at": session.UpdatedAt,
		}).Error
}

func (r *AgentSessionRepo) RecoverAgentSessions(ctx context.Context) error {
	return r.db.WithContext(ctx).Model(&agentSessionRow{}).
		Where("status = ?", model.AgentSessionRunning).
		Updates(map[string]any{
			"status": model.AgentSessionError, "last_error": "服务重启，上一次回答已中止",
			"updated_at": time.Now(),
		}).Error
}

func agentSessionToRow(session *model.AgentSession) *agentSessionRow {
	return &agentSessionRow{ID: session.ID, WorkspaceID: session.WorkspaceID, Title: session.Title,
		Status: session.Status, Context: string(session.Context), LastError: session.LastError,
		CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt}
}

func agentSessionToModel(row agentSessionRow) model.AgentSession {
	return model.AgentSession{ID: row.ID, WorkspaceID: row.WorkspaceID, Title: row.Title,
		Status: row.Status, Context: json.RawMessage(row.Context), LastError: row.LastError,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
