package port

import (
	"context"

	"reqflow/internal/domain/model"
)

// AgentConfigRepo 持久化数字大脑的工具开关和纯文本 Skill。
type AgentConfigRepo interface {
	ListAgentSkills(ctx context.Context, workspaceID string, enabledOnly bool) ([]model.AgentSkill, error)
	CreateAgentSkill(ctx context.Context, skill *model.AgentSkill) error
	SetAgentSkillEnabled(ctx context.Context, workspaceID, id string, enabled bool) error
	EnsureBuiltinAgentSkill(ctx context.Context, skill *model.AgentSkill) error
	ListAgentToolSettings(ctx context.Context, workspaceID string) ([]model.AgentToolSetting, error)
	SetAgentToolEnabled(ctx context.Context, workspaceID, toolName string, enabled bool) error
}
