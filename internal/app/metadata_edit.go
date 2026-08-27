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
	"strings"
	"time"

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

// Reload 从 metadata_registry 装载 override 并整体替换注册表合并层。
// 启动时调用一次；每次写路径后再次调用（写后立即读的失效保证）。
// effective 选择：每 (kind,key) 取最大 version 行，该行 disabled 则该 key 回退 seed。
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
	for _, row := range rows {
		if !row.Enabled {
			continue // 最新版被禁用 = 回退 seed（版本历史仍在库）
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
				schemas[sc.Type] = sc
			}
		case port.MetadataKindAnalyzeProfile:
			var p ProfilePayload
			if json.Unmarshal([]byte(row.Payload), &p) != nil {
				continue
			}
			profiles[row.Key] = profileOverride{Role: p.Role, Example: p.Example}
		}
	}
	setMetadataOverrides(schemas, profiles)
	return nil
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

	seed, _ := seedSchemaOf(in.DatasetType)
	rowVer, err := s.nextVersion(ctx, port.MetadataKindDatasetSchema, in.DatasetType)
	if err != nil {
		return nil, err
	}
	payload := in.Schema
	payload.Type = in.DatasetType
	payload.Version = seed.Version + rowVer
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

// checkSchema 共同前置：类型注册 → 形状校验 → 兼容判定 → 影响面清单。
func (s *MetadataService) checkSchema(ctx context.Context, in SchemaUpdateInput) (*SchemaUpdateResult, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("元数据存储未配置（metadata repo 为空）")
	}
	if _, ok := seedSchemaOf(in.DatasetType); !ok {
		return nil, fmt.Errorf("数据集类型 %s 未注册（无 seed 定义可覆盖）", in.DatasetType)
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
// effective 回 seed。已是 builtin 时幂等返回不落库。
func (s *MetadataService) ResetSchema(ctx context.Context, datasetType string) (*SchemaUpdateResult, error) {
	seed, ok := seedSchemaOf(datasetType)
	if !ok {
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

// History 版本历史（新→旧）。kind 限定 dataset_schema / analyze_profile。
func (s *MetadataService) History(ctx context.Context, kind, key string) ([]MetadataVersionView, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("元数据存储未配置（metadata repo 为空）")
	}
	switch kind {
	case port.MetadataKindDatasetSchema, port.MetadataKindAnalyzeProfile:
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
		view, err := s.TaskTypeView(d.Type)
		if err != nil {
			return nil, err
		}
		out.TaskTypes = append(out.TaskTypes, *view)
	}
	return out, nil
}

// MetadataImportItem 单个导入项（schema / profile 至少其一）。
type MetadataImportItem struct {
	Type    string               `json:"type"`
	Schema  *model.DatasetSchema `json:"schema,omitempty"`
	Profile *ProfilePayload      `json:"profile,omitempty"`
}

// MetadataImportInput 导入入参（逐项走 Update* 同一守卫）。
type MetadataImportInput struct {
	TaskTypes    []MetadataImportItem `json:"task_types"`
	ConfirmRisky bool                 `json:"confirm_risky"`
	Summary      string               `json:"summary"`
}

// MetadataImportItemResult 单项导入结果（Error 非空 = 该项被守卫拦截或失败，不中断其余）。
type MetadataImportItemResult struct {
	Type    string              `json:"type"`
	Schema  *SchemaUpdateResult `json:"schema,omitempty"`
	Profile *ProfileUpdateResult `json:"profile,omitempty"`
	Error   string              `json:"error,omitempty"`
}

// MetadataImportResult 导入汇总。
type MetadataImportResult struct {
	Items []MetadataImportItemResult `json:"items"`
}

// Import 导入：逐项复用 UpdateSchema/UpdateProfile（兼容守卫与审计同路径），
// 单项失败不中断。summary 缺省标 [import]。
func (s *MetadataService) Import(ctx context.Context, in MetadataImportInput) (*MetadataImportResult, error) {
	out := &MetadataImportResult{}
	for _, item := range in.TaskTypes {
		res := MetadataImportItemResult{Type: item.Type}
		summary := orDefault(in.Summary, "[import] 导入")
		if item.Schema != nil {
			d, ok := TaskTypeOf(item.Type)
			if !ok {
				res.Error = fmt.Sprintf("任务类型 %s 未注册", item.Type)
				out.Items = append(out.Items, res)
				continue
			}
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
		if res.Schema == nil && res.Profile == nil && res.Error == "" {
			res.Error = "该项无 schema/profile 载荷"
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
func summarizeReport(r logic.CompatReport) string {
	counts := map[logic.CompatLevel]int{}
	rules := map[string]bool{}
	for _, f := range r.Findings {
		counts[f.Level]++
		rules[f.Rule] = true
	}
	var parts []string
	for _, rule := range []string{"field_added", "field_added_required", "field_removed", "type_changed",
		"in_key_change", "in_vector_changed", "enum_expanded", "enum_narrowed", "text_changed", "required_tightened"} {
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
