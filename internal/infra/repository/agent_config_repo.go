package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"reqflow/internal/domain/model"
)

type AgentConfigRepo struct{ db *gorm.DB }

func NewAgentConfigRepo(db *gorm.DB) *AgentConfigRepo { return &AgentConfigRepo{db: db} }

type agentSkillRow struct {
	ID          string    `gorm:"column:id;primaryKey"`
	WorkspaceID string    `gorm:"column:workspace_id"`
	Slug        string    `gorm:"column:slug"`
	Title       string    `gorm:"column:title"`
	Description string    `gorm:"column:description"`
	Prompt      string    `gorm:"column:prompt"`
	Enabled     bool      `gorm:"column:enabled"`
	Builtin     bool      `gorm:"column:builtin"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (agentSkillRow) TableName() string { return "agent_skills" }

type agentToolSettingRow struct {
	WorkspaceID string    `gorm:"column:workspace_id;primaryKey"`
	ToolName    string    `gorm:"column:tool_name;primaryKey"`
	Enabled     bool      `gorm:"column:enabled"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (agentToolSettingRow) TableName() string { return "agent_tool_settings" }

func (r *AgentConfigRepo) ListAgentSkills(ctx context.Context, workspaceID string, enabledOnly bool) ([]model.AgentSkill, error) {
	query := r.db.WithContext(ctx).Where("workspace_id = ?", workspaceID)
	if enabledOnly {
		query = query.Where("enabled = TRUE")
	}
	var rows []agentSkillRow
	if err := query.Order("builtin DESC, title ASC, created_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.AgentSkill, len(rows))
	for i := range rows {
		out[i] = agentSkillToModel(rows[i])
	}
	return out, nil
}

func (r *AgentConfigRepo) CreateAgentSkill(ctx context.Context, skill *model.AgentSkill) error {
	if skill.ID == "" {
		skill.ID = uuid.NewString()
	}
	now := time.Now()
	skill.CreatedAt, skill.UpdatedAt = now, now
	return r.db.WithContext(ctx).Create(agentSkillFromModel(skill)).Error
}

func (r *AgentConfigRepo) SetAgentSkillEnabled(ctx context.Context, workspaceID, id string, enabled bool) error {
	result := r.db.WithContext(ctx).Model(&agentSkillRow{}).
		Where("workspace_id = ? AND id = ?", workspaceID, id).
		Updates(map[string]any{"enabled": enabled, "updated_at": time.Now()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *AgentConfigRepo) EnsureBuiltinAgentSkill(ctx context.Context, skill *model.AgentSkill) error {
	if skill.ID == "" {
		skill.ID = uuid.NewString()
	}
	now := time.Now()
	if skill.CreatedAt.IsZero() {
		skill.CreatedAt = now
	}
	skill.UpdatedAt = now
	row := agentSkillFromModel(skill)
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "workspace_id"}, {Name: "slug"}},
		DoUpdates: clause.Assignments(map[string]any{
			"title": skill.Title, "description": skill.Description, "prompt": skill.Prompt,
			"builtin": true,
		}),
	}).Create(row).Error
}

func (r *AgentConfigRepo) ListAgentToolSettings(ctx context.Context, workspaceID string) ([]model.AgentToolSetting, error) {
	var rows []agentToolSettingRow
	if err := r.db.WithContext(ctx).Where("workspace_id = ?", workspaceID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.AgentToolSetting, len(rows))
	for i := range rows {
		out[i] = model.AgentToolSetting{WorkspaceID: rows[i].WorkspaceID, ToolName: rows[i].ToolName,
			Enabled: rows[i].Enabled, UpdatedAt: rows[i].UpdatedAt}
	}
	return out, nil
}

func (r *AgentConfigRepo) SetAgentToolEnabled(ctx context.Context, workspaceID, toolName string, enabled bool) error {
	row := agentToolSettingRow{WorkspaceID: workspaceID, ToolName: toolName, Enabled: enabled, UpdatedAt: time.Now()}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "workspace_id"}, {Name: "tool_name"}},
		DoUpdates: clause.Assignments(map[string]any{"enabled": enabled, "updated_at": row.UpdatedAt}),
	}).Create(&row).Error
}

func agentSkillFromModel(skill *model.AgentSkill) *agentSkillRow {
	return &agentSkillRow{ID: skill.ID, WorkspaceID: skill.WorkspaceID, Slug: skill.Slug,
		Title: skill.Title, Description: skill.Description, Prompt: skill.Prompt,
		Enabled: skill.Enabled, Builtin: skill.Builtin, CreatedAt: skill.CreatedAt, UpdatedAt: skill.UpdatedAt}
}

func agentSkillToModel(row agentSkillRow) model.AgentSkill {
	return model.AgentSkill{ID: row.ID, WorkspaceID: row.WorkspaceID, Slug: row.Slug,
		Title: row.Title, Description: row.Description, Prompt: row.Prompt,
		Enabled: row.Enabled, Builtin: row.Builtin, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
