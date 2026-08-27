// 新任务类型向导用例（M4）：步骤链编排 + schema 字段定义 + 指令头填写 + 绑定声明，
// 一次提交注册出完整的任务类型聚合定义。
//
// M4 红线（要义见 HANDOVER「元数据系统不变量」）：向导产出的定义**先以 disabled 入库**——
// 工作流锚行（kind=workflow，key=任务类型）带 enabled=false 写入；运行时装载器
// （Reload→buildExtensionTypes）只认 enabled 锚行，草稿天然对运行时不可见。
// 人工验证（预览/详情）后经 SetWorkflowStatus 启用，类型才进入 TaskTypes 目录、
// 创建入口与分析链路。StepKind 封闭集由 logic.ValidateWorkflowShape 把守：
// 向导只能编排既有 kind，新 kind 执行器仍是代码开发。
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reqflow/internal/app/tools"
	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// TaskTypeWizardInput 向导提交的完整任务类型定义。
type TaskTypeWizardInput struct {
	Type        string              `json:"type"`         // 任务类型标识（tasks.type）
	DatasetType string              `json:"dataset_type"` // 产出数据集类型（随类型一起新建，不与既有类型共用）
	Workflow    model.Workflow      `json:"workflow"`     // 步骤链（封闭集 kind 编排）
	Schema      model.DatasetSchema `json:"schema"`       // 字段合同
	Role        string              `json:"role"`         // agent 指令头
	Example     string              `json:"example"`      // 单发示例（可空 → 按 schema 生成骨架）
	Summary     string              `json:"summary"`      // 变更说明（审计）
}

// WizardResult 向导提交结果。产物恒为草稿（enabled=false），启用走 SetWorkflowStatus。
type WizardResult struct {
	Type        string             `json:"type"`
	DatasetType string             `json:"dataset_type"`
	Draft       bool               `json:"draft"`
	Versions    map[string]int     `json:"versions"` // kind → 注册表行版本 {schema, profile, workflow}
	Report      logic.CompatReport `json:"report"`   // 编排/文案告警汇总（⚠️ 不阻塞落库——草稿未生效）
	Preview     *PromptPreview     `json:"preview"`  // 即时提示词预览（人工验证依据）
	Saved       bool               `json:"saved"`
	Summary     string             `json:"summary"` // 实际采用的审计说明
}

// RegisterTaskType 向导注册：整体校验 → 三行落库（schema/profile 生效态就绪，
// workflow 锚行 disabled）→ 审计 → 刷新合并层 → 返回即时预览。
// 已存在同名草稿时为替换式重提（版本续链）；已启用的同类型拒绝（改用组件编辑器）。
func (s *MetadataService) RegisterTaskType(ctx context.Context, in TaskTypeWizardInput) (*WizardResult, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("元数据存储未配置（metadata repo 为空）")
	}
	in.Type = strings.TrimSpace(in.Type)
	in.DatasetType = strings.TrimSpace(in.DatasetType)

	/* ---- 标识符与占用校验 ---- */
	if !logic.IsValidIdentifier(in.Type) {
		return nil, fmt.Errorf("任务类型标识 %q 非法（须为小写字母开头的 snake_case，≤63 字符）", in.Type)
	}
	if !logic.IsValidIdentifier(in.DatasetType) {
		return nil, fmt.Errorf("数据集类型 %q 非法（须为小写字母开头的 snake_case，≤63 字符）", in.DatasetType)
	}
	if _, seeded := seededTaskTypeOf(in.Type); seeded {
		return nil, fmt.Errorf("任务类型 %s 为内置类型，不可经向导重新注册", in.Type)
	}
	for _, d := range allDefinitionsOf() {
		if d.Type == in.Type {
			return nil, fmt.Errorf("任务类型 %s 已启用：向导仅供新类型注册，请使用字段合同/装配描述/工作流编辑器修改", in.Type)
		}
		if d.DatasetType == in.DatasetType {
			return nil, fmt.Errorf("数据集类型 %s 已被任务类型 %s 占用：数据集类型是任务间衔接的身份键，不允许两类型共写一个结果集", in.DatasetType, d.Type)
		}
	}

	/* ---- 构件校验 ---- */
	in.Schema.Type = in.DatasetType // 类型一致性由服务端强制，不依赖调用方
	if err := logic.ValidateSchemaShape(in.Schema); err != nil {
		return nil, err
	}
	if err := logic.ValidateProfileText(in.Role, in.Example); err != nil {
		return nil, err
	}
	wf := in.Workflow
	wf.Type = in.Type
	if err := logic.ValidateWorkflowShape(wf); err != nil {
		return nil, err
	}
	if logic.CountStepsOf(wf, model.StepKindAnalyze) == 0 {
		return nil, fmt.Errorf("步骤链至少需要一个 AI 分析步骤（analyze）：任务类型的成立条件是有分析产出")
	}

	res := &WizardResult{
		Type: in.Type, DatasetType: in.DatasetType,
		Versions: map[string]int{},
		Report:   logic.CheckWorkflowCompat(model.Workflow{}, wf),
	}
	res.Report.Findings = append(res.Report.Findings, profileAdvisories(in.Role, in.Example)...)
	recountReport(&res.Report)

	summary := orDefault(in.Summary, "[向导] 注册任务类型 "+in.Type)

	/* ---- 三行落库（顺序固定；中途失败留孤儿行无害——无锚行不装载） ---- */
	schemaPayload := in.Schema
	schemaPayload.Type = in.DatasetType
	schemaVer, err := s.nextVersion(ctx, port.MetadataKindDatasetSchema, in.DatasetType)
	if err != nil {
		return nil, err
	}
	schemaPayload.Version = schemaVer // 扩展类型无 seed 基线
	if err := s.repo.CreateEntry(ctx, &port.MetadataEntry{
		Kind: port.MetadataKindDatasetSchema, Key: in.DatasetType, Version: schemaVer,
		Payload: marshalJSON(schemaPayload), Enabled: true, Summary: summary + " · 字段合同",
	}); err != nil {
		return nil, fmt.Errorf("元数据写入失败: %w", err)
	}
	res.Versions["schema"] = schemaVer

	profileVer, err := s.nextVersion(ctx, port.MetadataKindAnalyzeProfile, in.Type)
	if err != nil {
		return nil, err
	}
	profileJSON, _ := json.Marshal(ProfilePayload{Role: in.Role, Example: in.Example})
	if err := s.repo.CreateEntry(ctx, &port.MetadataEntry{
		Kind: port.MetadataKindAnalyzeProfile, Key: in.Type, Version: profileVer,
		Payload: string(profileJSON), Enabled: true, Summary: summary + " · 装配描述",
	}); err != nil {
		return nil, fmt.Errorf("元数据写入失败: %w", err)
	}
	res.Versions["profile"] = profileVer

	anchorVer, err := s.nextVersion(ctx, port.MetadataKindWorkflow, in.Type)
	if err != nil {
		return nil, err
	}
	if err := s.repo.CreateEntry(ctx, &port.MetadataEntry{
		Kind: port.MetadataKindWorkflow, Key: in.Type, Version: anchorVer,
		Payload: marshalJSON(WorkflowPayload{DatasetType: in.DatasetType, Workflow: wf}),
		Enabled: false, Summary: summary + " · 工作流（草稿待启用）",
	}); err != nil {
		return nil, fmt.Errorf("元数据写入失败: %w", err)
	}
	res.Versions["workflow"] = anchorVer

	s.audit(ctx, "register_task_type", port.MetadataKindWorkflow, in.Type, 0, anchorVer, summary+"（草稿）")

	if err := s.Reload(ctx); err != nil {
		return nil, err
	}

	def := composeExtensionDefinition(in.Type, in.DatasetType, wf, schemaPayload, in.Role, in.Example)
	res.Preview = promptPreviewOf(def, "")
	res.Draft, res.Saved, res.Summary = true, true, summary
	return res, nil
}

/* ---- 草稿组合视图 ---- */

// draftBundle 草稿态的行组（disabled 的 workflow 锚行 + 配套 schema/profile 行）。
type draftBundle struct {
	Anchor  port.MetadataEntry
	Profile *port.MetadataEntry
	Schema  *port.MetadataEntry
}

// lookupDraftRows 取某任务类型的向导草稿行；不存在（或已是启用态）返回 false。
func (s *MetadataService) lookupDraftRows(taskType string) (*draftBundle, bool) {
	if s.repo == nil {
		return nil, false
	}
	rows, err := s.repo.LatestEntries(context.Background())
	if err != nil {
		return nil, false
	}
	var anchor *port.MetadataEntry
	var b draftBundle
	for i, row := range rows {
		rowCopy := rows[i]
		switch {
		case row.Kind == port.MetadataKindWorkflow && row.Key == taskType && !row.Enabled:
			cp := rowCopy
			anchor = &cp
			b.Anchor = rowCopy
		case row.Kind == port.MetadataKindAnalyzeProfile && row.Key == taskType:
			b.Profile = &rowCopy
		}
	}
	if anchor == nil {
		return nil, false
	}
	var payload WorkflowPayload
	if json.Unmarshal([]byte(anchor.Payload), &payload) == nil && payload.DatasetType != "" {
		for i, row := range rows {
			if row.Kind == port.MetadataKindDatasetSchema && row.Key == payload.DatasetType {
				cp := rows[i]
				b.Schema = &cp
				break
			}
		}
	}
	return &b, true
}

// definitionFromBundle 把草稿行组装成聚合定义（仅供详情视图与提示词预览；
// 不进运行时——运行时只认 Reload 装载的 enabled 锚行）。
func definitionFromBundle(typ string, b *draftBundle) (TaskTypeDefinition, error) {
	var wp WorkflowPayload
	if json.Unmarshal([]byte(b.Anchor.Payload), &wp) != nil || len(wp.Workflow.Steps) == 0 || wp.DatasetType == "" {
		return TaskTypeDefinition{}, fmt.Errorf("任务类型 %s 草稿的工作流载荷损坏", typ)
	}
	var sc model.DatasetSchema
	if b.Schema == nil || json.Unmarshal([]byte(b.Schema.Payload), &sc) != nil || sc.Type == "" {
		return TaskTypeDefinition{}, fmt.Errorf("任务类型 %s 草稿缺少字段合同定义", typ)
	}
	sc.Type, sc.Version = wp.DatasetType, b.Schema.Version
	var pp ProfilePayload
	if b.Profile == nil || json.Unmarshal([]byte(b.Profile.Payload), &pp) != nil {
		return TaskTypeDefinition{}, fmt.Errorf("任务类型 %s 草稿缺少装配描述", typ)
	}
	return composeExtensionDefinition(typ, wp.DatasetType, wp.Workflow, sc, pp.Role, pp.Example), nil
}

// composeExtensionDefinition 由构件组装扩展类型定义（向导即时预览与草稿视图共用）。
func composeExtensionDefinition(typ, datasetType string, wf model.Workflow, sc model.DatasetSchema, role, example string) TaskTypeDefinition {
	def := TaskTypeDefinition{
		Type:        typ,
		Workflow:    wf,
		DatasetType: datasetType,
		Schema:      func() model.DatasetSchema { return sc },
		Profile: AnalyzeProfile{
			Role:    role,
			Example: example,
			Write:   tools.DefaultWriteSpec(),
		},
	}
	def.Profile.Schema = def.Schema
	def.Profile.Write.Schema = sc
	return def
}

// WizardDraftView 草稿/启用状态的统一聚合视图（元数据页 detail 的草稿模式）。
func (s *MetadataService) WizardDraftView(taskType string) (*TaskTypeView, error) {
	b, ok := s.lookupDraftRows(taskType)
	if !ok {
		return nil, fmt.Errorf("任务类型 %s 无待启用草稿", taskType)
	}
	def, err := definitionFromBundle(taskType, b)
	if err != nil {
		return nil, err
	}
	view := s.taskTypeViewOf(def)
	view.Custom, view.Draft = true, true
	view.Source, view.SchemaSource, view.ProfileSource, view.WorkflowSource =
		MetadataSourceOverridden, MetadataSourceOverridden, MetadataSourceOverridden, MetadataSourceOverridden
	return view, nil
}

/* ---- 私有小件 ---- */

// profileAdvisories 指令头文案告警（与 UpdateProfile 同口径；草稿未生效仅提示不阻塞）。
func profileAdvisories(role, example string) []logic.CompatFinding {
	fs := []logic.CompatFinding{}
	if strings.TrimSpace(role) != "" && !strings.Contains(role, "{field_spec}") {
		fs = append(fs, logic.CompatFinding{Level: logic.CompatWarn, Rule: "field_spec_lost",
			Message: "指令头不含 {field_spec} 占位：schema 字段规范段将不会注入系统提示词（如非有意请保留）"})
	}
	if logic.HasTemplateBraces(role) || logic.HasTemplateBraces(example) {
		fs = append(fs, logic.CompatFinding{Level: logic.CompatWarn, Rule: "prompt_pattern",
			Message: "指令头/示例含 {{ 序列：疑似模板注入或粘贴了外部模板，请确认"})
	}
	return fs
}

// recountReport 重算汇总标志（findings 二次拼装后）。
func recountReport(r *logic.CompatReport) {
	r.Blocked, r.NeedsConfirm = false, false
	for _, f := range r.Findings {
		switch f.Level {
		case logic.CompatBlock:
			r.Blocked = true
		case logic.CompatWarn:
			r.NeedsConfirm = true
		}
	}
}
