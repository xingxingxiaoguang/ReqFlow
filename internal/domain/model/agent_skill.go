package model

import "time"

// AgentSkill 是可由斜杠命令激活的纯文本提示词模块。Skill 不保存脚本、文件或外部依赖。
type AgentSkill struct {
	ID          string
	WorkspaceID string
	Slug        string
	Title       string
	Description string
	Prompt      string
	Enabled     bool
	Builtin     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type AgentToolSetting struct {
	WorkspaceID string
	ToolName    string
	Enabled     bool
	UpdatedAt   time.Time
}
