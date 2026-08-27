package app

import (
	"fmt"
	"time"

	"reqflow/internal/app/agent"
	"reqflow/internal/app/tools"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// 元数据目录用例：聚合注册表（registry.go）的统一对外视图 + 提示词预览。
// 设计见 docs/METADATA.md——M1 全部条目 source=builtin；M3 引入 metadata_registry
// 覆盖与受控编辑后出现 overridden，读侧给 effective 合并视图（运行时仍进程内供给，
// 不经 HTTP），写路径见 metadata_edit.go（版本递增 + 兼容守卫 + 审计 + 导入导出）。
// 提示词预览复用运行时渲染器（prompt.go）与工具集构造（tools.BuildForRun），
// 与实际装配同一函数——不存在第二套渲染逻辑。

// 元数据条目来源（分层真相源三态中的前两态；effective 为运行时合并视图）。
const (
	MetadataSourceBuiltin    = "builtin"    // 代码内置（随二进制发布的出厂默认）
	MetadataSourceOverridden = "overridden" // 数据库覆盖（M3 受控编辑的产物）
)

// TaskTypeSummary 目录总览的单个任务类型条目。
type TaskTypeSummary struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Desc        string `json:"desc"`
	StepCount   int    `json:"step_count"`
	DatasetType string `json:"dataset_type,omitempty"`
	SchemaLabel string `json:"schema_label,omitempty"`
	Source      string `json:"source"`
}

// MetadataCatalog 目录总览（元数据页左栏）。
type MetadataCatalog struct {
	TaskTypes []TaskTypeSummary `json:"task_types"`
}

// MetadataWriteBinding 写入工具绑定声明（草稿经何工具提交、按何 schema 校验）。
type MetadataWriteBinding struct {
	ToolName string `json:"tool_name"`
}

// MetadataProfileView 装配描述视图。Role 为声明原文（含 {field_spec} 占位——
// 渲染后的实际形态见 PromptPreview，两处对照即「声明 → 装配」的全链路）。
type MetadataProfileView struct {
	Role    string               `json:"role"`
	Example string               `json:"example"`
	Write   MetadataWriteBinding `json:"write"`
}

// MetadataToolView agent 工具声明（snippet/guidelines 即系统提示词的同源素材）。
type MetadataToolView struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Snippet     string   `json:"snippet"`
	Guidelines  []string `json:"guidelines"`
}

// TaskTypeView 任务类型聚合视图（元数据页详情：workflow + schema + profile + 工具）。
// Workflow/Schema 直接透传 domain 类型（JSON 形状与 /workflows、/datasets/schemas
// 端点一致，前端复用既有 Workflow/DatasetSchema 类型）。
// Source 为整体来源（schema/profile 任一被覆盖即 overridden）；
// SchemaSource/ProfileSource 为分构件来源（M3 编辑器徽标与回退按钮的判定依据）。
type TaskTypeView struct {
	Type          string              `json:"type"`
	Name          string              `json:"name"`
	Desc          string              `json:"desc"`
	Source        string              `json:"source"`
	SchemaSource  string              `json:"schema_source"`
	ProfileSource string              `json:"profile_source"`
	DatasetType   string              `json:"dataset_type"`
	Workflow      model.Workflow      `json:"workflow"`
	Schema        model.DatasetSchema `json:"schema"`
	Profile       MetadataProfileView `json:"profile"`
	Tools         []MetadataToolView  `json:"tools"`
}

// PromptPreviewInput 提示词预览入参。
type PromptPreviewInput struct {
	TaskType string `json:"task_type" binding:"required"`
	Special  string `json:"special_requirements,omitempty"`
}

// PromptPreview 三段提示词的实时渲染（与运行时装配同一函数）。
type PromptPreview struct {
	TaskType          string `json:"task_type"`
	AgentSystemPrompt string `json:"agent_system_prompt"` // agent 模式系统提示词
	AgentFirstMessage string `json:"agent_first_message"` // 首轮文档清单（模板示意）
	ClassicPrompt     string `json:"classic_prompt"`      // 单发模式完整 prompt
}

// 预览的文档占位（预览无真实文档：首轮消息为模板示意，规模数字随实际文档；
// 单发 prompt 中标注全文拼入位置）。
const (
	previewDocName = "（文档名，如：需求文档 V1.2.docx）"
	previewDocText = "（需求文档全文将在分析时拼入此处）"
)

// MetadataService 元数据目录用例。
type MetadataService struct {
	repo     port.MetadataRepo // nil = 无 DB 覆盖层（纯 seed 运行；单测/降级形态）
	datasets port.DatasetRepo  // 兼容检查的影响面清单（存量数据集）
}

// NewMetadataService 构造用例。repo 允许 nil（无覆盖层时读侧退化为纯 seed）。
func NewMetadataService(repo port.MetadataRepo, datasets port.DatasetRepo) *MetadataService {
	return &MetadataService{repo: repo, datasets: datasets}
}

// Catalog 目录总览。
func (s *MetadataService) Catalog() MetadataCatalog {
	defs := TaskTypes()
	out := MetadataCatalog{TaskTypes: make([]TaskTypeSummary, 0, len(defs))}
	for _, d := range defs {
		source := MetadataSourceBuiltin
		if schemaOverridden(d.DatasetType) || profileOverridden(d.Type) {
			source = MetadataSourceOverridden
		}
		sum := TaskTypeSummary{
			Type: d.Type, Name: d.Workflow.Name, Desc: d.Workflow.Desc,
			StepCount: len(d.Workflow.Steps), DatasetType: d.DatasetType,
			Source: source,
		}
		if d.Schema != nil {
			sum.SchemaLabel = d.Schema().Label
		}
		out.TaskTypes = append(out.TaskTypes, sum)
	}
	return out
}

// TaskTypeView 聚合视图：workflow + schema + profile + 工具清单。
func (s *MetadataService) TaskTypeView(taskType string) (*TaskTypeView, error) {
	d, ok := TaskTypeOf(taskType)
	if !ok {
		return nil, fmt.Errorf("任务类型 %s 未注册（聚合注册表 TaskTypeOf）", taskType)
	}
	writeTool := d.Profile.Write.Name
	if writeTool == "" { // 零值绑定兜底（与 tools.WriteSpec.orDefault 同口径）
		writeTool = tools.DefaultWriteSpec().Name
	}
	schemaSource, profileSource := MetadataSourceBuiltin, MetadataSourceBuiltin
	if schemaOverridden(d.DatasetType) {
		schemaSource = MetadataSourceOverridden
	}
	if profileOverridden(d.Type) {
		profileSource = MetadataSourceOverridden
	}
	source := MetadataSourceBuiltin
	if schemaSource == MetadataSourceOverridden || profileSource == MetadataSourceOverridden {
		source = MetadataSourceOverridden
	}
	view := &TaskTypeView{
		Type: d.Type, Name: d.Workflow.Name, Desc: d.Workflow.Desc,
		Source: source, SchemaSource: schemaSource, ProfileSource: profileSource,
		DatasetType: d.DatasetType,
		Workflow:    d.Workflow,
		Profile: MetadataProfileView{
			Role:    d.Profile.Role,
			Example: d.Profile.Example,
			Write:   MetadataWriteBinding{ToolName: writeTool},
		},
	}
	if d.Schema != nil {
		view.Schema = d.Schema()
	}
	for _, t := range previewToolset(d) {
		tv := MetadataToolView{Name: t.Spec().Name, Description: t.Spec().Description}
		if dt, ok := t.(agent.DocumentedTool); ok {
			tv.Snippet, tv.Guidelines = dt.PromptSnippet(), dt.PromptGuidelines()
		}
		view.Tools = append(view.Tools, tv)
	}
	return view, nil
}

// PromptPreview 按当前注册定义实时渲染三段提示词（与运行时装配同函数）。
func (s *MetadataService) PromptPreview(in PromptPreviewInput) (*PromptPreview, error) {
	d, ok := TaskTypeOf(in.TaskType)
	if !ok {
		return nil, fmt.Errorf("任务类型 %s 未注册（聚合注册表 TaskTypeOf）", in.TaskType)
	}
	now := time.Now()
	return &PromptPreview{
		TaskType:          d.Type,
		AgentSystemPrompt: renderAgentSystem(now, in.Special, previewToolset(d), d.Profile),
		AgentFirstMessage: renderDocManifest(tools.DocSource{FileName: previewDocName}),
		ClassicPrompt:     renderAnalyzePrompt(previewDocText, now, in.Special, d.Profile),
	}, nil
}

// previewToolset 以零值运行依赖构造工具集（空文档/空 sink/无交互桥）：
// 与运行时 buildToolset 同一构造函数，预览即装配的精确复现。
func previewToolset(d TaskTypeDefinition) []agent.Tool {
	return buildToolset(AnalyzeInput{}, tools.NewDraftSink(), d.Profile)
}
