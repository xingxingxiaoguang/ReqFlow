package app

// 元数据受控编辑用例（M3）：seed → override → effective 三态生效的写路径。
//
//	启动：Reload 从 metadata_registry 装载 override 进注册表合并层（registry.go）；
//	写：  UpdateSchema/UpdateProfile = 形状校验 → 兼容规则引擎（logic.CheckSchemaCompat）
//	      → ❌ 拦截 / ⚠️ 需 confirm_risky → 版本递增落库 → 审计 → Reload 刷新；
//	回退：Reset* 写入 enabled=false 的最新版（版本历史不删，effective 回 seed）；
//	流转：Export 导出 effective 视图 / Import 走同一写路径逐项校验落库。
//
// 快照隔离（METADATA §4.5）：任务工作流快照与会话 schema 快照（port.Context.TaskSchema）
// 保证存量/进行中任务不受热编辑影响——本文件只管新任务生效。

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"reqflow/internal/app/tools"
	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// ProfilePayload analyze_profile 覆盖的载荷形状（指令头/示例；写入绑定是代码资产不外置）。
type ProfilePayload struct {
	Role    string `json:"role"`
	Example string `json:"example"`
}

/* ---- 合并加载器 ---- */

// WorkflowPayload workflow 覆盖的载荷形状（key = 任务类型；M4 同时承担
// 向导注册类型的「锚行」角色——dataset_type 声明该类型产出的数据集）。
type WorkflowPayload struct {
	DatasetType string          `json:"dataset_type"`
	Workflow    model.Workflow  `json:"workflow"`
}

// Reload 从 metadata_registry 装载 override 并整体替换注册表合并层。
// 启动时调用一次；每次写路径后再次调用（写后立即读的失效保证）。
// effective 选择：每 (kind,key) 取最大 version 行，该行 disabled 则该 key 回退 seed。
// 向导扩展类型（kind=workflow 锚行 + 同源 schema/profile 行）：锚行 enabled 且
// 三构件齐备才装载进运行时——草稿态天然对运行时不可见。
func (s *MetadataService) Reload(ctx context.Context) error {
	if s.repo == nil {
		return nil
	}
	rows, err := s.repo.LatestEntries(ctx)
	if err != nil {
		return fmt.Errorf("元数据覆盖层加载失败: %w", err)
	}
	schemas := map[string]model.DatasetSchema{}
	profiles := map[string]profileOverride{}
	workflows := map[string]model.Workflow{}
	wfPayloads := map[string]WorkflowPayload{}
	for _, row := range rows {
		if !row.Enabled {
			continue // 最新版被禁用 = 回退 seed（或扩展类型停在草稿态；版本历史仍在库）
		}
		switch row.Kind {
		case port.MetadataKindDatasetSchema:
			var sc model.DatasetSchema
			if json.Unmarshal([]byte(row.Payload), &sc) != nil || sc.Type == "" {
				continue // 坏载荷跳过（不让单条坏数据拖垮启动）
			}
			if seed, ok := seedSchemaOf(sc.Type); ok {
				// effective 版本 = seed 基线 + 注册表版本（seed 升版只抬基线，不回退）
				sc.Version = seed.Version + row.Version
			} else {
				sc.Version = row.Version // 向导扩展类型无 seed 基线
			}
			schemas[sc.Type] = sc
		case port.MetadataKindAnalyzeProfile:
			var p ProfilePayload
			if json.Unmarshal([]byte(row.Payload), &p) != nil {
				continue
			}
			profiles[row.Key] = profileOverride{Role: p.Role, Example: p.Example}
		case port.MetadataKindWorkflow:
			var p WorkflowPayload
			if json.Unmarshal([]byte(row.Payload), &p) != nil || len(p.Workflow.Steps) == 0 {
				continue
			}
			workflows[row.Key] = p.Workflow
			wfPayloads[row.Key] = p
		}
	}
	extDefs := buildExtensionTypes(workflows, wfPayloads, schemas, profiles)
	setMetadataOverrides(schemas, profiles, workflows, extDefs)
	return nil
}

// buildExtensionTypes 从锚行构造向导注册的任务类型（无 seed 基线，构件全出自
// 注册表行）。缺 schema/profile 构件的不完整锚行跳过并告警——宁可整型缺席，
// 不让半成品定义进入运行时。
func buildExtensionTypes(workflows map[string]model.Workflow, wfPayloads map[string]WorkflowPayload,
	schemas map[string]model.DatasetSchema, profiles map[string]profileOverride) []TaskTypeDefinition {
	types := make([]string, 0, len(workflows))
	for typ := range workflows {
		if _, seeded := seededTaskTypeOf(typ); seeded {
			continue // seed 类型的 workflow 覆盖不是扩展类型
		}
		types = append(types, typ)
	}
	sort.Strings(types)
	out := make([]TaskTypeDefinition, 0, len(types))
	for _, typ := range types {
		payload := wfPayloads[typ]
		sc, hasSchema := schemas[payload.DatasetType]
		if payload.DatasetType == "" || !hasSchema {
			slog.Warn("元数据覆盖层：任务类型缺少产出 schema 定义，跳过装载", "task_type", typ)
			continue
		}
		p, hasProfile := profiles[typ]
		if !hasProfile {
			slog.Warn("元数据覆盖层：任务类型缺少装配描述，跳过装载", "task_type", typ)
			continue
		}
		def := TaskTypeDefinition{
			Type:        typ,
			Workflow:    workflows[typ],
			DatasetType: payload.DatasetType,
			Schema:      func() model.DatasetSchema { return sc },
			Profile: AnalyzeProfile{
				Role:    p.Role,
				Example: p.Example,
				Schema:  func() model.DatasetSchema { return sc },
				Write:   tools.DefaultWriteSpec(),
			},
		}
		def.Profile.Write.Schema = sc
		out = append(out, def)
	}
	return out
}

/* ---- schema 写路径 ---- */

// SchemaUpdateInput schema 受控编辑入参（:type = 数据集类型）。
type SchemaUpdateInput struct {
	DatasetType  string              `json:"-"` // 路径参数注入
	Schema       model.DatasetSchema `json:"schema"`
	ConfirmRisky bool                `json:"confirm_risky"` // ⚠️ 项的显式确认
	Summary      string              `json:"summary"`       // 变更说明（审计）
}

// AffectedDataset 兼容影响面涉及的存量数据集。
type AffectedDataset struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ItemCount     int    `json:"item_count"`
	SchemaVersion int    `json:"schema_version"`
	NeedsReembed  bool   `json:"needs_reembed"` // InVector 变更类：语料向量需重嵌
}

// SchemaUpdateResult check（dry-run）与 update（保存）的统一返回。
// Saved=false 表示未落库（check 调用 / 被 ❌ 拦截 / ⚠️ 待确认）。
type SchemaUpdateResult struct {
	DatasetType string               `json:"dataset_type"`
	Version     int                  `json:"version"` // check = 当前 effective 版本；update 成功 = 新版本
	Source      string               `json:"source"`
	Report      logic.CompatReport   `json:"report"`
	Datasets    []AffectedDataset    `json:"datasets"`
	Saved       bool                 `json:"saved"`
	BlockReason string               `json:"block_reason,omitempty"`
}

// CheckSchema 兼容性检查 dry-run：对存量数据集判定影响面，不落库。
func (s *MetadataService) CheckSchema(ctx context.Context, in SchemaUpdateInput) (*SchemaUpdateResult, error) {
	res, err := s.checkSchema(ctx, in)
	if err != nil {
		return nil, err
	}
	return res, nil // Saved 恒 false
}

// UpdateSchema schema 受控编辑：形状校验 → 兼容守卫 → 版本递增 + 审计 → 刷新 effective。
// 返回 (result, err)：err 非 nil 且 result 非 nil = 守卫拦截（409，携带判定明细）。
func (s *MetadataService) UpdateSchema(ctx context.Context, in SchemaUpdateInput) (*SchemaUpdateResult, error) {
	res, err := s.checkSchema(ctx, in)
	if err != nil {
		return nil, err
	}
	if res.Report.Blocked {
		res.BlockReason = "存在不兼容变更（❌），保存被拦截"
		return res, fmt.Errorf("%s", res.BlockReason)
	}
	if res.Report.NeedsConfirm && !in.ConfirmRisky {
		res.BlockReason = "存在需确认的影响项（⚠️），请确认影响面后携带 confirm_risky 重试"
		return res, fmt.Errorf("%s", res.BlockReason)
	}

	base := 0 // 向导扩展类型无 seed 基线（base=0，版本即注册表行版本）
	if seed, ok := seedSchemaOf(in.DatasetType); ok {
		base = seed.Version
	}
	rowVer, err := s.nextVersion(ctx, port.MetadataKindDatasetSchema, in.DatasetType)
	if err != nil {
		return nil, err
	}
	payload := in.Schema
	payload.Type = in.DatasetType
	payload.Version = base + rowVer
	if err := s.repo.CreateEntry(ctx, &port.MetadataEntry{
		Kind: port.MetadataKindDatasetSchema, Key: in.DatasetType, Version: rowVer,
		Payload: marshalJSON(payload), Enabled: true, Summary: orDefault(in.Summary, summarizeReport(res.Report)),
	}); err != nil {
		return nil, fmt.Errorf("元数据写入失败: %w", err)
	}
	s.audit(ctx, "update_schema", port.MetadataKindDatasetSchema, in.DatasetType,
		res.Version, payload.Version, orDefault(in.Summary, summarizeReport(res.Report)))
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}
	res.Version, res.Source, res.Saved = payload.Version, MetadataSourceOverridden, true
	return res, nil
}

// registeredEffectiveDatasetType 数据集类型是否有可覆盖的 effective 定义
// （seed 注册，或向导扩展类型且锚行已启用）。向导草稿（锚行 disabled）不在其列
// ——草稿修整走重新提交向导，不经组件级编辑器。
func registeredEffectiveDatasetType(datasetType string) bool {
	if _, ok := seedSchemaOf(datasetType); ok {
		return true
	}
	for _, d := range currentExtensionTypes() {
		if d.DatasetType == datasetType {
			return true
		}
	}
	return false
}

// checkSchema 共同前置：类型可覆盖 → 形状校验 → 兼容判定 → 影响面清单。
func (s *MetadataService) checkSchema(ctx context.Context, in SchemaUpdateInput) (*SchemaUpdateResult, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("元数据存储未配置（metadata repo 为空）")
	}
	if !registeredEffectiveDatasetType(in.DatasetType) {
		return nil, fmt.Errorf("数据集类型 %s 未注册（若为向导草稿请重新提交向导修改）", in.DatasetType)
	}
	next := in.Schema
	next.Type = in.DatasetType
	if err := logic.ValidateSchemaShape(next); err != nil {
		return nil, err
	}
	old, _ := effectiveSchemaOf(in.DatasetType)
	res := &SchemaUpdateResult{
		DatasetType: in.DatasetType,
		Version:     old.Version,
		Source:      MetadataSourceBuiltin,
		Report:      logic.CheckSchemaCompat(old, next),
	}
	if schemaOverridden(in.DatasetType) {
		res.Source = MetadataSourceOverridden
	}
	if s.datasets != nil {
		list, err := s.datasets.ListDatasets(ctx, in.DatasetType, 50)
		if err == nil {
			for _, d := range list {
				if d.ItemCount <= 0 {
					continue
				}
				res.Datasets = append(res.Datasets, AffectedDataset{
					ID: d.ID, Name: d.Name, ItemCount: d.ItemCount,
					SchemaVersion: d.SchemaVersion, NeedsReembed: res.Report.NeedsReembed,
				})
			}
		}
		// 影响面清单查询失败不阻塞判定（守卫依据是规则引擎，不是清单）
	}
	return res, nil
}

// ResetSchema 回退到内置定义：写入 enabled=false 的最新版（版本历史保留），
// effective 回 seed。已是 builtin 时幂等返回不落库。仅限有 seed 基线的类型——
// 向导扩展类型无「内置」可回退（停用走 SetWorkflowStatus）。
func (s *MetadataService) ResetSchema(ctx context.Context, datasetType string) (*SchemaUpdateResult, error) {
	seed, ok := seedSchemaOf(datasetType)
	if !ok {
		if registeredEffectiveDatasetType(datasetType) {
			return nil, fmt.Errorf("数据集类型 %s 为向导注册类型，无内置定义可回退", datasetType)
		}
		return nil, fmt.Errorf("数据集类型 %s 未注册", datasetType)
	}
	res := &SchemaUpdateResult{DatasetType: datasetType, Version: seed.Version,
		Source: MetadataSourceBuiltin, Report: logic.CompatReport{Findings: []logic.CompatFinding{{
			Level: logic.CompatOK, Rule: "reset", Message: "回退到内置（seed）定义",
		}}}}
	if s.repo == nil || !schemaOverridden(datasetType) {
		return res, nil // 无覆盖：幂等
	}
	old, _ := effectiveSchemaOf(datasetType)
	rowVer, err := s.nextVersion(ctx, port.MetadataKindDatasetSchema, datasetType)
	if err != nil {
		return nil, err
	}
	payload := seed // 载荷存回退目标的 seed 定义（审计可追溯回退到了什么）
	if err := s.repo.CreateEntry(ctx, &port.MetadataEntry{
		Kind: port.MetadataKindDatasetSchema, Key: datasetType, Version: rowVer,
		Payload: marshalJSON(payload), Enabled: false, Summary: "回退到内置定义",
	}); err != nil {
		return nil, fmt.Errorf("元数据写入失败: %w", err)
	}
	s.audit(ctx, "reset_schema", port.MetadataKindDatasetSchema, datasetType, old.Version, seed.Version, "回退到内置定义")
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}
	res.Saved = true
	return res, nil
}

/* ---- profile 写路径 ---- */

// ProfileUpdateInput 装配描述编辑入参（:type = 任务类型）。
type ProfileUpdateInput struct {
	TaskType string `json:"-"`
	Role     string `json:"role"`
	Example  string `json:"example"`
	Summary  string `json:"summary"`
}

// ProfileUpdateResult profile 编辑返回（文案层无硬拦截；Findings 为告警项）。
type ProfileUpdateResult struct {
	TaskType string                `json:"task_type"`
	Version  int                   `json:"version"`
	Source   string                `json:"source"`
	Findings []logic.CompatFinding `json:"findings"`
	Saved    bool                  `json:"saved"`
}

// UpdateProfile 指令头/示例编辑（影响面 = 新任务提示词；兼容规则表 ✅ 纯文案层）。
func (s *MetadataService) UpdateProfile(ctx context.Context, in ProfileUpdateInput) (*ProfileUpdateResult, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("元数据存储未配置（metadata repo 为空）")
	}
	d, ok := TaskTypeOf(in.TaskType)
	if !ok {
		return nil, fmt.Errorf("任务类型 %s 未注册", in.TaskType)
	}
	if err := logic.ValidateProfileText(in.Role, in.Example); err != nil {
		return nil, err
	}
	res := &ProfileUpdateResult{TaskType: in.TaskType, Source: MetadataSourceOverridden}
	// 文案层告警：{field_spec} 占位丢失 / 模板注入模式
	if strings.Contains(d.Profile.Role, "{field_spec}") && !strings.Contains(in.Role, "{field_spec}") {
		res.Findings = append(res.Findings, logic.CompatFinding{Level: logic.CompatWarn, Rule: "field_spec_lost",
			Message: "新指令头不含 {field_spec} 占位：schema 字段规范段将不会注入系统提示词（如非有意请保留）"})
	}
	if logic.HasTemplateBraces(in.Role) || logic.HasTemplateBraces(in.Example) {
		res.Findings = append(res.Findings, logic.CompatFinding{Level: logic.CompatWarn, Rule: "prompt_pattern",
			Message: "指令头/示例含 {{ 序列：疑似模板注入或粘贴了外部模板，请确认"})
	}

	rowVer, err := s.nextVersion(ctx, port.MetadataKindAnalyzeProfile, in.TaskType)
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(ProfilePayload{Role: in.Role, Example: in.Example})
	if err := s.repo.CreateEntry(ctx, &port.MetadataEntry{
		Kind: port.MetadataKindAnalyzeProfile, Key: in.TaskType, Version: rowVer,
		Payload: string(payload), Enabled: true, Summary: orDefault(in.Summary, "编辑装配描述"),
	}); err != nil {
		return nil, fmt.Errorf("元数据写入失败: %w", err)
	}
	s.audit(ctx, "update_profile", port.MetadataKindAnalyzeProfile, in.TaskType, rowVer-1, rowVer, orDefault(in.Summary, "编辑装配描述"))
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}
	res.Version, res.Saved = rowVer, true
	return res, nil
}

// ResetProfile 回退装配描述到内置（幂等；版本历史保留）。
func (s *MetadataService) ResetProfile(ctx context.Context, taskType string) (*ProfileUpdateResult, error) {
	d, ok := TaskTypeOf(taskType)
	if !ok {
		return nil, fmt.Errorf("任务类型 %s 未注册", taskType)
	}
	res := &ProfileUpdateResult{TaskType: taskType, Source: MetadataSourceBuiltin,
		Findings: []logic.CompatFinding{{Level: logic.CompatOK, Rule: "reset", Message: "回退到内置（seed）定义"}}}
	if s.repo == nil || !profileOverridden(taskType) {
		return res, nil
	}
	rowVer, err := s.nextVersion(ctx, port.MetadataKindAnalyzeProfile, taskType)
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(ProfilePayload{Role: d.Profile.Role, Example: d.Profile.Example})
	if err := s.repo.CreateEntry(ctx, &port.MetadataEntry{
		Kind: port.MetadataKindAnalyzeProfile, Key: taskType, Version: rowVer,
		Payload: string(payload), Enabled: false, Summary: "回退到内置定义",
	}); err != nil {
		return nil, fmt.Errorf("元数据写入失败: %w", err)
	}
	s.audit(ctx, "reset_profile", port.MetadataKindAnalyzeProfile, taskType, rowVer-1, 0, "回退到内置定义")
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}
	res.Saved = true
	return res, nil
}

/* ---- 工作流写路径（M4：定义外置 + 快照不变量） ---- */

// WorkflowUpdateInput 工作流编辑入参（:type = 任务类型）。
type WorkflowUpdateInput struct {
	TaskType     string         `json:"-"` // 路径参数注入
	Workflow     model.Workflow `json:"workflow"`
	ConfirmRisky bool           `json:"confirm_risky"` // ⚠️ 项的显式确认
	Summary      string         `json:"summary"`
}

// WorkflowUpdateResult check（dry-run）与 update 的统一返回。
// 工作流变更借快照机制只影响新任务，判定全部是 ✅/⚠️（无 ❌ 硬拦截）。
type WorkflowUpdateResult struct {
	TaskType    string             `json:"task_type"`
	Version     int                `json:"version"` // 注册表行版本（builtin 时为 0）
	Source      string             `json:"source"`
	Report      logic.CompatReport `json:"report"`
	Saved       bool               `json:"saved"`
	BlockReason string             `json:"block_reason,omitempty"`
}

// CheckWorkflow 工作流兼容检查 dry-run：形状校验 + 与当前 effective 定义比对。
func (s *MetadataService) CheckWorkflow(in WorkflowUpdateInput) (*WorkflowUpdateResult, error) {
	return s.checkWorkflow(in)
}

// UpdateWorkflow 工作流受控编辑：形状校验 → 兼容判定 → 版本递增 + 审计 → 刷新。
// 存量任务不受影响（tasks.workflow 创建时快照）；新任务经 WorkflowOf 读到本定义。
func (s *MetadataService) UpdateWorkflow(ctx context.Context, in WorkflowUpdateInput) (*WorkflowUpdateResult, error) {
	res, err := s.checkWorkflow(in)
	if err != nil {
		return nil, err
	}
	if res.Report.NeedsConfirm && !in.ConfirmRisky {
		res.BlockReason = "存在需确认的影响项（⚠️），请确认影响面后携带 confirm_risky 重试"
		return res, fmt.Errorf("%s", res.BlockReason)
	}
	d, _ := TaskTypeOf(in.TaskType) // checkWorkflow 已确保可解析
	rowVer, err := s.nextVersion(ctx, port.MetadataKindWorkflow, in.TaskType)
	if err != nil {
		return nil, err
	}
	payload := WorkflowPayload{DatasetType: d.DatasetType, Workflow: in.Workflow}
	payload.Workflow.Type = in.TaskType
	if err := s.repo.CreateEntry(ctx, &port.MetadataEntry{
		Kind: port.MetadataKindWorkflow, Key: in.TaskType, Version: rowVer,
		Payload: marshalJSON(payload), Enabled: true,
		Summary: orDefault(in.Summary, summarizeFindings(res.Report.Findings)),
	}); err != nil {
		return nil, fmt.Errorf("元数据写入失败: %w", err)
	}
	s.audit(ctx, "update_workflow", port.MetadataKindWorkflow, in.TaskType, rowVer-1, rowVer,
		orDefault(in.Summary, summarizeFindings(res.Report.Findings)))
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}
	res.Version, res.Source, res.Saved = rowVer, MetadataSourceOverridden, true
	return res, nil
}

// checkWorkflow 共同前置：类型 live → 形状校验 → 与 effective 对比。
func (s *MetadataService) checkWorkflow(in WorkflowUpdateInput) (*WorkflowUpdateResult, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("元数据存储未配置（metadata repo 为空）")
	}
	d, ok := TaskTypeOf(in.TaskType)
	if !ok {
		hint := ""
		if _, rowsExist := s.lookupDraftRows(in.TaskType); rowsExist {
			hint = "（该类型为向导草稿，请重新提交向导修改或在工作台启用后再编辑）"
		}
		return nil, fmt.Errorf("任务类型 %s 未注册%s", in.TaskType, hint)
	}
	next := in.Workflow
	next.Type = in.TaskType
	if err := logic.ValidateWorkflowShape(next); err != nil {
		return nil, err
	}
	old := d.Workflow
	old.Type = in.TaskType
	source := MetadataSourceBuiltin
	if workflowOverridden(in.TaskType) {
		source = MetadataSourceOverridden
	}
	version := 0
	if source == MetadataSourceOverridden {
		if rows, err := s.repo.ListVersions(context.Background(), port.MetadataKindWorkflow, in.TaskType, 1); err == nil && len(rows) > 0 {
			version = rows[0].Version
		}
	}
	return &WorkflowUpdateResult{
		TaskType: in.TaskType, Version: version, Source: source,
		Report: logic.CheckWorkflowCompat(old, next),
	}, nil
}

// SetWorkflowStatus 启用/停用向导注册的任务类型（就地翻转锚行 enabled，内容版本不动）。
// 内置类型不提供停用——其 override 行的启停语义是「回退 seed」，属 ResetWorkflow 辖域。
func (s *MetadataService) SetWorkflowStatus(ctx context.Context, taskType string, enabled bool) (*WorkflowUpdateResult, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("元数据存储未配置（metadata repo 为空）")
	}
	if !customTaskType(taskType) {
		return nil, fmt.Errorf("内置任务类型 %s 不支持启用/停用（如需自定义步骤链请编辑工作流）", taskType)
	}
	rows, err := s.repo.ListVersions(ctx, port.MetadataKindWorkflow, taskType, 1)
	if err != nil || len(rows) == 0 {
		return nil, fmt.Errorf("任务类型 %s 未注册", taskType)
	}
	currentlyEnabled := rows[0].Enabled
	if currentlyEnabled == enabled {
		return &WorkflowUpdateResult{TaskType: taskType, Version: rows[0].Version, Saved: false}, nil // 幂等
	}
	if err := s.repo.UpdateLatestEnabled(ctx, port.MetadataKindWorkflow, taskType, enabled); err != nil {
		return nil, fmt.Errorf("状态更新失败: %w", err)
	}
	action, summary := "enable_task_type", "启用任务类型"
	if !enabled {
		action, summary = "disable_task_type", "停用任务类型"
	}
	s.audit(ctx, action, port.MetadataKindWorkflow, taskType, rows[0].Version, rows[0].Version, summary)
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}
	return &WorkflowUpdateResult{TaskType: taskType, Version: rows[0].Version, Source: MetadataSourceOverridden, Saved: true}, nil
}

// ResetWorkflow 回退内置类型的工作流覆盖到 seed（幂等；版本历史保留）。
func (s *MetadataService) ResetWorkflow(ctx context.Context, taskType string) (*WorkflowUpdateResult, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("元数据存储未配置（metadata repo 为空）")
	}
	if !customTaskType(taskType) {
		if _, ok := TaskTypeOf(taskType); !ok {
			return nil, fmt.Errorf("任务类型 %s 未注册", taskType)
		}
	} else {
		return nil, fmt.Errorf("向导注册类型 %s 无内置工作流可回退（停用请用状态切换）", taskType)
	}
	seedDef, ok := seededTaskTypeOf(taskType)
	if !ok {
		return nil, fmt.Errorf("任务类型 %s 无 seed 工作流定义", taskType)
	}
	res := &WorkflowUpdateResult{TaskType: taskType, Source: MetadataSourceBuiltin,
		Report: logic.CompatReport{Findings: []logic.CompatFinding{{
			Level: logic.CompatOK, Rule: "reset", Message: "回退到内置（seed）工作流",
		}}}}
	if !workflowOverridden(taskType) {
		return res, nil // 无覆盖：幂等
	}
	oldVersion := 0
	if rows, err := s.repo.ListVersions(context.Background(), port.MetadataKindWorkflow, taskType, 1); err == nil && len(rows) > 0 {
		oldVersion = rows[0].Version
	}
	rowVer, err := s.nextVersion(ctx, port.MetadataKindWorkflow, taskType)
	if err != nil {
		return nil, err
	}
	payload := WorkflowPayload{DatasetType: seedDef.DatasetType, Workflow: seedDef.Workflow}
	if err := s.repo.CreateEntry(ctx, &port.MetadataEntry{
		Kind: port.MetadataKindWorkflow, Key: taskType, Version: rowVer,
		Payload: marshalJSON(payload), Enabled: false, Summary: "回退到内置工作流",
	}); err != nil {
		return nil, fmt.Errorf("元数据写入失败: %w", err)
	}
	s.audit(ctx, "reset_workflow", port.MetadataKindWorkflow, taskType, oldVersion, 0, "回退到内置工作流")
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}
	res.Saved = true
	return res, nil
}

/* ---- 版本历史 ---- */

// MetadataVersionView 版本历史条目（payload 为该版完整定义原文）。
type MetadataVersionView struct {
	Version          int             `json:"version"`
	Enabled          bool            `json:"enabled"`
	EffectiveVersion int             `json:"effective_version"` // dataset_schema = seed 基线 + 行版本
	Summary          string          `json:"summary"`
	CreatedBy        string          `json:"created_by"`
	CreatedAt        time.Time       `json:"created_at"`
	Payload          json.RawMessage `json:"payload"`
}

// History 版本历史（新→旧）。kind 限定 dataset_schema / analyze_profile / workflow。
func (s *MetadataService) History(ctx context.Context, kind, key string) ([]MetadataVersionView, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("元数据存储未配置（metadata repo 为空）")
	}
	switch kind {
	case port.MetadataKindDatasetSchema, port.MetadataKindAnalyzeProfile, port.MetadataKindWorkflow:
	default:
		return nil, fmt.Errorf("未知的元数据种类: %s", kind)
	}
	rows, err := s.repo.ListVersions(ctx, kind, key, 100)
	if err != nil {
		return nil, err
	}
	out := make([]MetadataVersionView, 0, len(rows))
	for _, row := range rows {
		v := MetadataVersionView{
			Version: row.Version, Enabled: row.Enabled,
			Summary: row.Summary, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt,
			Payload: json.RawMessage(row.Payload),
		}
		v.EffectiveVersion = row.Version
		if kind == port.MetadataKindDatasetSchema {
			if seed, ok := seedSchemaOf(key); ok {
				v.EffectiveVersion = seed.Version + row.Version
			}
		}
		out = append(out, v)
	}
	return out, nil
}

/* ---- 导出 / 导入 ---- */

// MetadataExport effective 视图导出（DX 语义：回 git 留档/跨环境分发人工导入）。
type MetadataExport struct {
	ExportedAt time.Time       `json:"exported_at"`
	TaskTypes  []TaskTypeView  `json:"task_types"`
}

// Export 导出当前 effective 聚合视图。
func (s *MetadataService) Export() (*MetadataExport, error) {
	out := &MetadataExport{ExportedAt: time.Now()}
	for _, d := range TaskTypes() {
		view := s.taskTypeViewOf(d)
		if _, ok := TaskTypeOf(d.Type); ok {
			view.Custom = customTaskType(d.Type)
		}
		out.TaskTypes = append(out.TaskTypes, *view)
	}
	return out, nil
}

// MetadataImportItem 单个导入项（schema / profile / workflow 至少其一；
// workflow 需配套 schema+profile——三件齐备的新类型按向导注册为草稿）。
type MetadataImportItem struct {
	Type        string               `json:"type"`
	DatasetType string               `json:"dataset_type,omitempty"` // 新类型注册必需
	Schema      *model.DatasetSchema `json:"schema,omitempty"`
	Profile     *ProfilePayload      `json:"profile,omitempty"`
	Workflow    *model.Workflow      `json:"workflow,omitempty"`
}

// MetadataImportInput 导入入参（逐项走 Update* 同一守卫）。
type MetadataImportInput struct {
	TaskTypes    []MetadataImportItem `json:"task_types"`
	ConfirmRisky bool                 `json:"confirm_risky"`
	Summary      string               `json:"summary"`
}

// MetadataImportItemResult 单项导入结果（Error 非空 = 该项被守卫拦截或失败，不中断其余）。
type MetadataImportItemResult struct {
	Type     string               `json:"type"`
	Schema   *SchemaUpdateResult  `json:"schema,omitempty"`
	Profile  *ProfileUpdateResult `json:"profile,omitempty"`
	Workflow *WorkflowUpdateResult `json:"workflow,omitempty"`
	Drafted  bool                 `json:"drafted,omitempty"` // 新类型：已按向导注册为草稿（待人工启用）
	Error    string               `json:"error,omitempty"`
}

// MetadataImportResult 导入汇总。
type MetadataImportResult struct {
	Items []MetadataImportItemResult `json:"items"`
}

// Import 导入：存量项逐个复用 UpdateSchema/UpdateProfile/UpdateWorkflow（守卫与审计同路径）；
// 三件齐备的全新类型按向导注册为**草稿**（导入不直接生效，人工验证后启用）。
// 单项失败不中断。summary 缺省标 [import]。
func (s *MetadataService) Import(ctx context.Context, in MetadataImportInput) (*MetadataImportResult, error) {
	out := &MetadataImportResult{}
	for _, item := range in.TaskTypes {
		res := MetadataImportItemResult{Type: item.Type}
		summary := orDefault(in.Summary, "[import] 导入")
		if _, live := TaskTypeOf(item.Type); !live {
			// 全新类型：走向导注册（产物恒为草稿）
			if item.Workflow == nil || item.Schema == nil || item.Profile == nil {
				res.Error = fmt.Sprintf("任务类型 %s 未注册：新类型导入需 workflow+schema+profile 三件齐备", item.Type)
				out.Items = append(out.Items, res)
				continue
			}
			if _, err := s.RegisterTaskType(ctx, TaskTypeWizardInput{
				Type:        item.Type,
				DatasetType: item.DatasetType,
				Workflow:    *item.Workflow,
				Schema:      *item.Schema,
				Role:        item.Profile.Role,
				Example:     item.Profile.Example,
				Summary:     summary,
			}); err != nil {
				res.Error = err.Error()
			} else {
				res.Drafted = true
			}
			out.Items = append(out.Items, res)
			continue
		}
		d, _ := TaskTypeOf(item.Type) // 已确认 live（上方分支排除未注册）
		if item.Schema != nil {
			sr, err := s.UpdateSchema(ctx, SchemaUpdateInput{
				DatasetType: d.DatasetType, Schema: *item.Schema,
				ConfirmRisky: in.ConfirmRisky, Summary: summary,
			})
			res.Schema = sr
			if err != nil {
				res.Error = err.Error()
			}
		}
		if item.Profile != nil {
			pr, err := s.UpdateProfile(ctx, ProfileUpdateInput{
				TaskType: item.Type, Role: item.Profile.Role, Example: item.Profile.Example, Summary: summary,
			})
			res.Profile = pr
			if err != nil && res.Error == "" {
				res.Error = err.Error()
			}
		}
		if item.Workflow != nil {
			wr, err := s.UpdateWorkflow(ctx, WorkflowUpdateInput{
				TaskType: item.Type, Workflow: *item.Workflow,
				ConfirmRisky: in.ConfirmRisky, Summary: summary,
			})
			res.Workflow = wr
			if err != nil && res.Error == "" {
				res.Error = err.Error()
			}
		}
		if res.Schema == nil && res.Profile == nil && res.Workflow == nil && res.Error == "" {
			res.Error = "该项无 schema/profile/workflow 载荷"
		}
		out.Items = append(out.Items, res)
	}
	return out, nil
}

/* ---- 私有 ---- */

// nextVersion 同 (kind,key) 下一版本号（现有最大 + 1；空 = 1）。
func (s *MetadataService) nextVersion(ctx context.Context, kind, key string) (int, error) {
	rows, err := s.repo.ListVersions(ctx, kind, key, 1)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 1, nil
	}
	return rows[0].Version + 1, nil
}

// audit 写审计（失败只影响审计完整性，不回滚业务写——审计表故障以日志暴露）。
func (s *MetadataService) audit(ctx context.Context, action, kind, key string, from, to int, summary string) {
	if err := s.repo.WriteAudit(ctx, &port.MetadataAuditEntry{
		Action: action, Kind: kind, Key: key, FromVersion: from, ToVersion: to, Summary: summary,
	}); err != nil {
		slog.Warn("元数据审计写入失败", "action", action, "kind", kind, "key", key, "err", err)
	}
}

// summarizeReport 变更说明缺省值：按判定汇总（审计可读性）。
func summarizeReport(r logic.CompatReport) string { return summarizeFindings(r.Findings) }

// summarizeFindings 变更说明缺省值：按规则标识去重汇总。
func summarizeFindings(fs []logic.CompatFinding) string {
	counts := map[logic.CompatLevel]int{}
	rules := map[string]bool{}
	for _, f := range fs {
		counts[f.Level]++
		rules[f.Rule] = true
	}
	var parts []string
	for _, rule := range []string{"field_added", "field_added_required", "field_removed", "type_changed",
		"in_key_change", "in_vector_changed", "enum_expanded", "enum_narrowed", "text_changed", "required_tightened",
		"step_added", "step_removed", "gate_removed", "kind_changed", "output_missing", "multi_analyze"} {
		if rules[rule] {
			parts = append(parts, rule)
		}
	}
	if len(parts) == 0 {
		return "无结构变更"
	}
	return fmt.Sprintf("%s（⚠️×%d）", strings.Join(parts, ", "), counts[logic.CompatWarn])
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) != "" {
		return s
	}
	return def
}
