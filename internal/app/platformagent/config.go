package platformagent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

const (
	defaultWorkspaceID  = "default"
	maxSkillTitleRunes  = 80
	maxSkillDescRunes   = 500
	maxSkillPromptRunes = 30000
)

var skillSlugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

type toolDefinition struct {
	Name        string
	Label       string
	Group       string
	Description string
}

var agentToolDefinitions = []toolDefinition{
	{Name: "list_workflows", Label: "查询流程", Group: "流程工具", Description: "查询草稿或已发布流程，以及输入端口和步骤概况。"},
	{Name: "create_workflow", Label: "创建流程", Group: "流程工具", Description: "创建经过依赖、端口和执行器校验的流程。"},
	{Name: "list_tasks", Label: "查询任务", Group: "任务工具", Description: "查询业务任务及当前执行状态。"},
	{Name: "create_task", Label: "创建任务", Group: "任务工具", Description: "从已发布流程和资源绑定创建任务。"},
	{Name: "run_task", Label: "运行任务", Group: "任务工具", Description: "启动待执行任务并读取运行快照。"},
	{Name: "query_data", Label: "查询数据", Group: "数据工具", Description: "发现数据集和索引，或执行关键词、语义与混合检索。"},
	{Name: "index_dataset", Label: "建立索引", Group: "数据工具", Description: "为数据集选择索引规则并启动索引任务。"},
	{Name: "create_skill", Label: "创建 Skill", Group: "Skill 工具", Description: "创建一个可通过斜杠命令复用的纯文本 Skill。"},
}

var builtinCreateSkill = model.AgentSkill{
	Slug:        "create-skill",
	Title:       "创建 Skill",
	Description: "把稳定的工作方法整理为可复用的纯文本 Skill",
	Prompt: `你正在帮助用户设计 ReqFlow 数字大脑的纯文本 Skill。

先理解并补齐 Skill 的目标、适用场景、输入信息、处理步骤、输出格式与边界。把提示词写成可直接执行、结构清晰的工作说明。当前平台只支持纯文本提示词，不支持脚本、附件、依赖包或外部文件，因此不要设计任何脚本执行步骤。

先向用户展示建议的 slug、标题、简介和完整提示词。只有用户明确确认创建时，才调用 create_skill 工具落库；不要把讨论中的草稿直接保存。创建完成后报告斜杠命令 /slug，并说明可在 Agent 设置中停用。`,
	Enabled: true,
	Builtin: true,
}

type ToolConfigView struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Group       string `json:"group"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type SkillView struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Prompt      string    `json:"prompt"`
	Enabled     bool      `json:"enabled"`
	Builtin     bool      `json:"builtin"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ConfigView struct {
	Tools  []ToolConfigView `json:"tools"`
	Skills []SkillView      `json:"skills"`
}

type CreateSkillInput struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Prompt      string `json:"prompt"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

func normalizeWorkspaceID(workspaceID string) string {
	if workspaceID = strings.TrimSpace(workspaceID); workspaceID != "" {
		return workspaceID
	}
	return defaultWorkspaceID
}

func (s *Service) ensureBuiltinSkills(ctx context.Context, workspaceID string) error {
	skill := builtinCreateSkill
	skill.WorkspaceID = normalizeWorkspaceID(workspaceID)
	return s.configRepo.EnsureBuiltinAgentSkill(ctx, &skill)
}

func (s *Service) GetConfig(ctx context.Context, workspaceID string) (*ConfigView, error) {
	workspaceID = normalizeWorkspaceID(workspaceID)
	if err := s.ensureBuiltinSkills(ctx, workspaceID); err != nil {
		return nil, err
	}
	settings, err := s.configRepo.ListAgentToolSettings(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	toolEnabled := make(map[string]bool, len(settings))
	for _, setting := range settings {
		toolEnabled[setting.ToolName] = setting.Enabled
	}
	tools := make([]ToolConfigView, len(agentToolDefinitions))
	for i, definition := range agentToolDefinitions {
		enabled, configured := toolEnabled[definition.Name]
		if !configured {
			enabled = true
		}
		tools[i] = ToolConfigView{Name: definition.Name, Label: definition.Label, Group: definition.Group,
			Description: definition.Description, Enabled: enabled}
	}
	skills, err := s.configRepo.ListAgentSkills(ctx, workspaceID, false)
	if err != nil {
		return nil, err
	}
	views := make([]SkillView, len(skills))
	for i := range skills {
		views[i] = skillView(skills[i])
	}
	return &ConfigView{Tools: tools, Skills: views}, nil
}

func (s *Service) CreateSkill(ctx context.Context, workspaceID string, input CreateSkillInput) (*SkillView, error) {
	skill, err := createAgentSkill(ctx, s.configRepo, normalizeWorkspaceID(workspaceID), input)
	if err != nil {
		return nil, err
	}
	view := skillView(*skill)
	return &view, nil
}

func (s *Service) SetSkillEnabled(ctx context.Context, workspaceID, id string, enabled bool) error {
	workspaceID, id = normalizeWorkspaceID(workspaceID), strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("skill_id 不能为空")
	}
	return s.configRepo.SetAgentSkillEnabled(ctx, workspaceID, id, enabled)
}

func (s *Service) SetToolEnabled(ctx context.Context, workspaceID, name string, enabled bool) error {
	workspaceID, name = normalizeWorkspaceID(workspaceID), strings.TrimSpace(name)
	if !knownTool(name) {
		return fmt.Errorf("未知 Agent 工具: %s", name)
	}
	return s.configRepo.SetAgentToolEnabled(ctx, workspaceID, name, enabled)
}

func createAgentSkill(ctx context.Context, repo port.AgentConfigRepo, workspaceID string, input CreateSkillInput) (*model.AgentSkill, error) {
	input.Slug = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(input.Slug, "/")))
	input.Title, input.Description, input.Prompt = strings.TrimSpace(input.Title),
		strings.TrimSpace(input.Description), strings.TrimSpace(input.Prompt)
	if !skillSlugPattern.MatchString(input.Slug) {
		return nil, fmt.Errorf("Skill slug 必须以小写字母开头，只能包含小写字母、数字和连字符，最长 48 个字符")
	}
	if len([]rune(input.Title)) == 0 || len([]rune(input.Title)) > maxSkillTitleRunes {
		return nil, fmt.Errorf("Skill 标题必须为 1..%d 个字符", maxSkillTitleRunes)
	}
	if len([]rune(input.Description)) > maxSkillDescRunes {
		return nil, fmt.Errorf("Skill 简介不能超过 %d 个字符", maxSkillDescRunes)
	}
	if len([]rune(input.Prompt)) == 0 || len([]rune(input.Prompt)) > maxSkillPromptRunes {
		return nil, fmt.Errorf("Skill 提示词必须为 1..%d 个字符", maxSkillPromptRunes)
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	skill := &model.AgentSkill{WorkspaceID: workspaceID, Slug: input.Slug, Title: input.Title,
		Description: input.Description, Prompt: input.Prompt, Enabled: enabled}
	if err := repo.CreateAgentSkill(ctx, skill); err != nil {
		return nil, fmt.Errorf("创建 Skill /%s 失败: %w", input.Slug, err)
	}
	return skill, nil
}

func skillView(skill model.AgentSkill) SkillView {
	return SkillView{ID: skill.ID, Slug: skill.Slug, Title: skill.Title, Description: skill.Description,
		Prompt: skill.Prompt, Enabled: skill.Enabled, Builtin: skill.Builtin,
		CreatedAt: skill.CreatedAt, UpdatedAt: skill.UpdatedAt}
}

func knownTool(name string) bool {
	for _, definition := range agentToolDefinitions {
		if definition.Name == name {
			return true
		}
	}
	return false
}

func enabledToolSettings(settings []model.AgentToolSetting) map[string]bool {
	out := make(map[string]bool, len(settings))
	for _, setting := range settings {
		out[setting.ToolName] = setting.Enabled
	}
	return out
}
