package app

// M4 向导与工作流外置测试：草稿生命周期（注册→disabled→启用→停用）、
// 冲突校验（seed 类型/数据集类型占用/标识符）、工作流编辑守卫（⚠️ 确认流）、
// 快照隔离（存量任务按快照、新任务跟随 effective）、导入扩展。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

/* ---- 测试输入构造 ---- */

func wizardSchema() model.DatasetSchema {
	return model.DatasetSchema{
		Label: "评审记录",
		Fields: []model.FieldSpec{
			{Key: "title", Label: "标题", Type: model.FieldString, Required: true, InKey: true, InVector: model.VectorTitle, Prompt: "一句话概括问题"},
			{Key: "severity", Label: "严重度", Type: model.FieldEnum, Enum: []string{"High", "Medium", "Low"}, Default: "Medium"},
			{Key: "finding", Label: "问题描述", Type: model.FieldText, InVector: model.VectorBody},
		},
	}
}

func wizardWorkflow() model.Workflow {
	return model.Workflow{
		Type: "test_review", Name: "评审导入",
		Desc: "上传评审纪要 → AI 提取 issue → 生成评审数据集",
		Steps: []model.WorkflowStep{
			{Seq: 1, Name: "上传解析", Kind: model.StepKindParse},
			{Seq: 2, Name: "AI 分析", Kind: model.StepKindAnalyze},
			{Seq: 3, Name: "生成数据集", Kind: model.StepKindDataset},
		},
	}
}

func wizardInput() TaskTypeWizardInput {
	sc := wizardSchema()
	wf := wizardWorkflow()
	return TaskTypeWizardInput{
		Type: "test_review", DatasetType: "review",
		Schema: sc, Workflow: wf,
		Role:    "你是评审助手。\n\n{field_spec}\n\n提取所有 issue。",
		Example: `[{"title":"空指针","severity":"High","finding":"未判空"}]`,
		Summary: "[向导] 测试注册",
	}
}

/* ---- 生命周期 ---- */

func TestWizardLifecycleDraftToEnabled(t *testing.T) {
	svc, repo := newEditSvc(t)
	ctx := context.Background()

	res, err := svc.RegisterTaskType(ctx, wizardInput())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Draft || !res.Saved {
		t.Fatalf("产物应为已保存草稿: %+v", res)
	}
	if res.Versions["workflow"] != 1 || res.Versions["schema"] != 1 || res.Versions["profile"] != 1 {
		t.Fatalf("三行版本应从 1 起: %v", res.Versions)
	}
	// 即时预览可用（人工验证依据）
	if res.Preview == nil || !strings.Contains(res.Preview.AgentSystemPrompt, "issue") {
		t.Fatal("向导结果应含即时提示词预览")
	}
	// 草稿对运行时不可见
	if _, ok := TaskTypeOf("test_review"); ok {
		t.Fatal("草稿不应进入运行时目录")
	}
	for _, d := range TaskTypes() {
		if d.Type == "test_review" {
			t.Fatal("草稿不应出现在 TaskTypes")
		}
	}
	// 但详情视图（include_draft）可读，供验证入口
	view, err := svc.TaskTypeView("test_review", true)
	if err != nil {
		t.Fatalf("草稿视图应可读: %v", err)
	}
	if !view.Draft || !view.Custom || view.Name != "评审导入" || len(view.Workflow.Steps) != 3 {
		t.Fatalf("草稿视图形状不对: %+v", view)
	}
	if view.Schema.Type != "review" {
		t.Fatalf("草稿视图 schema 应来自草稿行: %+v", view.Schema)
	}
	if _, err := svc.TaskTypeView("test_review", false); err == nil {
		t.Fatal("不含 include_draft 时草稿不可读")
	}
	// 目录：主列表无、草稿组有
	cat := svc.Catalog(ctx)
	for _, s := range cat.TaskTypes {
		if s.Type == "test_review" {
			t.Fatal("草稿不应进目录主列表")
		}
	}
	if len(cat.DraftTypes) != 1 || cat.DraftTypes[0].Type != "test_review" || !cat.DraftTypes[0].Draft {
		t.Fatalf("目录应有草稿组条目: %+v", cat.DraftTypes)
	}

	// 启用 → 生效
	if _, err := svc.SetWorkflowStatus(ctx, "test_review", true); err != nil {
		t.Fatal(err)
	}
	d, ok := TaskTypeOf("test_review")
	if !ok || d.DatasetType != "review" || d.Workflow.Name != "评审导入" {
		t.Fatalf("启用后应可解析聚合定义: %+v %+v", ok, d)
	}
	if v := d.Schema(); v.Version != 1 || v.Label != "评审记录" {
		t.Fatalf("启用后 schema 生效: %+v", v)
	}
	if p, err := metadataAgentProfileFor("test_review"); err != nil || p.Write.Schema.Type != "review" {
		t.Fatalf("启用后 profile/写入绑定生效: %v %+v", err, p)
	}
	if sc, ok := effectiveSchemaOf("review"); !ok || sc.Type != "review" {
		t.Fatal("读侧 effective schema 应含向导类型")
	}
	schemas := effectiveSchemas()
	hit := false
	for _, sc := range schemas {
		if sc.Type == "review" {
			hit = true
		}
	}
	if !hit {
		t.Fatal("schema 目录应含向导类型的产出合同")
	}
	// 目录：进主列表（custom 徽标）、草稿组清空
	cat = svc.Catalog(ctx)
	inMain := false
	for _, s := range cat.TaskTypes {
		if s.Type == "test_review" {
			inMain = s.Custom && !s.Draft
		}
	}
	if !inMain || len(cat.DraftTypes) != 0 {
		t.Fatalf("启用后应在主列表且草稿组清空: %+v", cat)
	}
	// 审计链
	audits := mustAudits(t, repo, port.MetadataKindWorkflow, "test_review")
	if len(audits) != 2 || audits[0].Action != "enable_task_type" || audits[1].Action != "register_task_type" {
		t.Fatalf("审计顺序不对: %+v", audits)
	}

	// 停用 → 运行时下线；再停用幂等
	if _, err := svc.SetWorkflowStatus(ctx, "test_review", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := TaskTypeOf("test_review"); ok {
		t.Fatal("停用后不应在运行时")
	}
	res2, err := svc.SetWorkflowStatus(ctx, "test_review", false)
	if err != nil || res2.Saved {
		t.Fatalf("重复停用应幂等不落库: %v %+v", err, res2)
	}
	// 详情仍以草稿形态可读（重新启用的入口）
	view, err = svc.TaskTypeView("test_review", true)
	if err != nil || !view.Draft {
		t.Fatalf("停用后应回草稿视图: %v %+v", err, view)
	}
}

func mustAudits(t *testing.T, repo *memMetadata, kind, key string) []port.MetadataAuditEntry {
	t.Helper()
	list, err := repo.ListAudit(context.Background(), kind, key, 10)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestWizardValidationMatrix(t *testing.T) {
	svc, _ := newEditSvc(t)
	ctx := context.Background()

	// 内置任务类型
	in := wizardInput()
	in.Type = model.TaskTypeRequirementImport
	if _, err := svc.RegisterTaskType(ctx, in); err == nil || !strings.Contains(err.Error(), "内置") {
		t.Fatalf("内置任务类型应拒绝: %v", err)
	}
	// 数据集类型占用
	in = wizardInput()
	in.DatasetType = model.DatasetTypeRequirement
	if _, err := svc.RegisterTaskType(ctx, in); err == nil || !strings.Contains(err.Error(), "已被任务类型") {
		t.Fatalf("数据集类型占用应拒绝: %v", err)
	}
	// 缺分析步骤
	in = wizardInput()
	in.Workflow.Steps = in.Workflow.Steps[:1]
	if _, err := svc.RegisterTaskType(ctx, in); err == nil || !strings.Contains(err.Error(), "分析步骤") {
		t.Fatalf("缺分析步骤应拒绝: %v", err)
	}
	// 未知 kind
	in = wizardInput()
	in.Workflow.Steps[1].Kind = model.StepKind("deploy")
	if _, err := svc.RegisterTaskType(ctx, in); err == nil || !strings.Contains(err.Error(), "封闭集合") {
		t.Fatalf("未知 kind 应拒绝: %v", err)
	}
	// 字段 key 非法（注入面收口）
	in = wizardInput()
	in.Schema.Fields[0].Key = "Bad Key"
	if _, err := svc.RegisterTaskType(ctx, in); err == nil {
		t.Fatal("非法字段 key 应拒绝")
	}
	// 指令头丢 {field_spec} → ⚠️ 告警但不阻塞（草稿态）
	in = wizardInput()
	in.Role = "你是评审助手，直接提取。"
	res, err := svc.RegisterTaskType(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range res.Report.Findings {
		if f.Rule == "field_spec_lost" && f.Level == "warn" {
			found = true
		}
	}
	if !found {
		t.Fatalf("应含 field_spec_lost 告警: %+v", res.Report.Findings)
	}
}

func TestWizardDraftResubmitVersionChain(t *testing.T) {
	svc, repo := newEditSvc(t)
	ctx := context.Background()
	if _, err := svc.RegisterTaskType(ctx, wizardInput()); err != nil {
		t.Fatal(err)
	}
	in := wizardInput()
	in.Schema.Fields = append(in.Schema.Fields,
		model.FieldSpec{Key: "owner", Label: "负责人", Type: model.FieldString})
	res, err := svc.RegisterTaskType(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Versions["schema"] != 2 || res.Versions["profile"] != 2 || res.Versions["workflow"] != 2 {
		t.Fatalf("重提应版本续链: %v", res.Versions)
	}
	// 新增字段在草稿视图中可见
	view, err := svc.TaskTypeView("test_review", true)
	if err != nil || len(view.Schema.Fields) != 4 {
		t.Fatalf("重提后草稿视图应含新字段: %v %+v", err, view)
	}
	versions, err := svc.History(ctx, port.MetadataKindDatasetSchema, "review")
	if err != nil || len(versions) != 2 {
		t.Fatalf("版本历史应保留两版: %v %d", err, len(versions))
	}
	_ = repo
}

func TestWizardRejectsResubmitWhenLive(t *testing.T) {
	svc, _ := newEditSvc(t)
	ctx := context.Background()
	if _, err := svc.RegisterTaskType(ctx, wizardInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetWorkflowStatus(ctx, "test_review", true); err != nil {
		t.Fatal(err)
	}
	err := RegisterTaskTypeErrorOf(svc, ctx)
	if err == nil || !strings.Contains(err.Error(), "已启用") {
		t.Fatalf("已启用类型再走向导应拒绝: %v", err)
	}
}

func RegisterTaskTypeErrorOf(svc *MetadataService, ctx context.Context) error {
	_, err := svc.RegisterTaskType(ctx, wizardInput())
	return err
}

/* ---- 工作流写路径与快照隔离 ---- */

func workflowUpdateInput(wf model.Workflow) WorkflowUpdateInput {
	return WorkflowUpdateInput{TaskType: model.TaskTypeRequirementImport, Workflow: wf, Summary: "测试改工作流"}
}

func seededWorkflow() model.Workflow {
	d, ok := TaskTypeOf(model.TaskTypeRequirementImport)
	if !ok {
		return model.Workflow{} // 未注册时零值由调用方断言兜底
	}
	return d.Workflow
}

func TestUpdateWorkflowSeededOverrideAndSnapshotIsolation(t *testing.T) {
	svc, repo := newEditSvc(t)
	ctx := context.Background()
	oldWf := seededWorkflow()

	// 无变更 dry-run：ok，不落库
	check, err := svc.CheckWorkflow(workflowUpdateInput(oldWf))
	if err != nil || check.Saved || check.Source != MetadataSourceBuiltin {
		t.Fatalf("dry-run 应为 builtin 且不落库: %v %+v", err, check)
	}
	// 移除人工门 → ⚠️ 需确认，未 confirm 被拦（res 携带判定）
	nw := oldWf
	nw.Steps = []model.WorkflowStep{
		{Seq: 1, Name: "上传解析", Kind: model.StepKindParse},
		{Seq: 2, Name: "AI 分析", Kind: model.StepKindAnalyze},
		{Seq: 3, Name: "生成数据集", Kind: model.StepKindDataset},
	}
	blocked, err := svc.UpdateWorkflow(ctx, workflowUpdateInput(nw))
	if err == nil || blocked == nil || blocked.Saved || !blocked.Report.NeedsConfirm || blocked.BlockReason == "" {
		t.Fatalf("移除人工门需确认拦截: %v %+v", err, blocked)
	}
	// confirm 后落库
	in := workflowUpdateInput(nw)
	in.ConfirmRisky = true
	res, err := svc.UpdateWorkflow(ctx, in)
	if err != nil || !res.Saved || res.Version != 1 {
		t.Fatalf("确认后应保存 v1: %v %+v", err, res)
	}
	// effective 视图跟随
	got, ok := WorkflowOf(model.TaskTypeRequirementImport)
	if !ok || len(got.Steps) != 3 {
		t.Fatalf("WorkflowOf 应读到覆盖定义: %v %+v", ok, got)
	}
	// 快照隔离：存量任务按 tasks.workflow 快照执行展示（ParseWorkflow 回退前）
	snapshotted := &model.Task{Type: model.TaskTypeRequirementImport, Workflow: MarshalWorkflow(oldWf)}
	if w := ParseWorkflow(snapshotted); len(w.Steps) != 4 {
		t.Fatalf("存量任务应按快照（4 步）: %d", len(w.Steps))
	}
	// 新任务走 effective：Create 的取数入口（WorkflowOf）已验证为 3 步覆盖链
	// 元数据目录的 WorkflowOf 查找仍应只暴露已启用定义。
	audits := mustAudits(t, repo, port.MetadataKindWorkflow, model.TaskTypeRequirementImport)
	if len(audits) != 1 || audits[0].Action != "update_workflow" || audits[0].FromVersion != 0 || audits[0].ToVersion != 1 {
		t.Fatalf("update_workflow 审计应对: %+v", audits)
	}
	// 回退 seed：幂等双击、历史保留、effective 回 4 步
	r1, err := svc.ResetWorkflow(ctx, model.TaskTypeRequirementImport)
	if err != nil || !r1.Saved {
		t.Fatalf("回退应保存: %v %+v", err, r1)
	}
	r2, err := svc.ResetWorkflow(ctx, model.TaskTypeRequirementImport)
	if err != nil || r2.Saved {
		t.Fatalf("builtin 态再回退应幂等: %v %+v", err, r2)
	}
	if wf, _ := WorkflowOf(model.TaskTypeRequirementImport); len(wf.Steps) != 4 {
		t.Fatal("回退后应回 seed 四步链")
	}
	history, err := svc.History(ctx, port.MetadataKindWorkflow, model.TaskTypeRequirementImport)
	if err != nil || len(history) != 2 || history[0].Enabled {
		t.Fatalf("回退行应 disabled 且历史保留: %v %+v", err, history)
	}
}

func TestUpdateWorkflowShapeGuard(t *testing.T) {
	svc, _ := newEditSvc(t)
	nw := seededWorkflow()
	nw.Steps[0].Kind = model.StepKind("deploy")
	if _, err := svc.CheckWorkflow(workflowUpdateInput(nw)); err == nil || !strings.Contains(err.Error(), "封闭集合") {
		t.Fatalf("未知 kind 应被形状校验拦下: %v", err)
	}
	nw = seededWorkflow()
	nw.Steps[2].Seq = 9 // 序号断裂
	if _, err := svc.CheckWorkflow(workflowUpdateInput(nw)); err == nil || !strings.Contains(err.Error(), "连续") {
		t.Fatalf("序号断裂应拦下: %v", err)
	}
}

func TestSetWorkflowStatusGuards(t *testing.T) {
	svc, _ := newEditSvc(t)
	ctx := context.Background()
	// 内置类型不允许启停
	if _, err := svc.SetWorkflowStatus(ctx, model.TaskTypeRequirementImport, false); err == nil || !strings.Contains(err.Error(), "不支持") {
		t.Fatalf("内置类型启停应拒绝: %v", err)
	}
	// 不存在的自定义类型
	if _, err := svc.SetWorkflowStatus(ctx, "nope_type", true); err == nil {
		t.Fatal("未注册类型应报错")
	}
	// ResetWorkflow 对内置类型无覆盖时幂等
	res, err := svc.ResetWorkflow(ctx, model.TaskTypeRequirementImport)
	if err != nil || res.Saved {
		t.Fatalf("无覆盖回退应幂等: %v %+v", err, res)
	}
	// ResetWorkflow 对草稿类型给出明确指引
	if _, err := svc.RegisterTaskType(ctx, wizardInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResetWorkflow(ctx, "test_review"); err == nil || !strings.Contains(err.Error(), "状态切换") {
		t.Fatalf("草稿类型回退应指引状态切换: %v", err)
	}
	// 草稿类型 UpdateWorkflow 给出明确指引
	errHint := UpdateWorkflowHintOf(svc)
	if errHint == nil || !strings.Contains(errHint.Error(), "向导") {
		t.Fatalf("草稿工作流编辑应指引向导: %v", errHint)
	}
}

func UpdateWorkflowHintOf(svc *MetadataService) error {
	nw := wizardWorkflow()
	nw.Steps = append(nw.Steps, model.WorkflowStep{Seq: 4, Name: "补充确认", Kind: model.StepKindHuman})
	_, err := svc.UpdateWorkflow(context.Background(),
		WorkflowUpdateInput{TaskType: "test_review", Workflow: nw})
	return err
}

/* ---- 导入扩展 ---- */

func TestImportWithNewTypeAndWorkflow(t *testing.T) {
	svc, _ := newEditSvc(t)
	ctx := context.Background()

	// 三件齐备的全新类型 → 注册为草稿
	in := MetadataImportInput{TaskTypes: []MetadataImportItem{{
		Type: "imported_kind", DatasetType: "imported_ds",
		Schema: ptrSchema(wizardSchema()), Profile: &ProfilePayload{Role: "你是导入测试。\n{field_spec}"},
		Workflow: ptrWorkflow(wizardWorkflow()),
	}}, Summary: "[import] 全量"}
	in.TaskTypes[0].Workflow.Type = "imported_kind"
	res, err := svc.Import(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || !res.Items[0].Drafted || res.Items[0].Error != "" {
		t.Fatalf("新类型应注册为草稿: %+v", res.Items)
	}
	if _, ok := TaskTypeOf("imported_kind"); ok {
		t.Fatal("导入的类型应为草稿（不直接生效）")
	}
	// 存量类型带 workflow → 走 UpdateWorkflow 守卫（无 ⚠️ 时直存）
	wf := seededWorkflow()
	wf.Desc = "描述更新版"
	res, err = svc.Import(ctx, MetadataImportInput{TaskTypes: []MetadataImportItem{
		{Type: model.TaskTypeRequirementImport, Workflow: &wf},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Items[0].Workflow == nil || !res.Items[0].Workflow.Saved {
		t.Fatalf("存量类型 workflow 导入应保存: %+v", res.Items[0])
	}
	if d, _ := TaskTypeOf(model.TaskTypeRequirementImport); d.Workflow.Desc != "描述更新版" {
		t.Fatalf("导入的工作流应生效: %+v", d.Workflow)
	}
	// 导出的聚合视图应包含工作流来源标注
	exp, err := svc.Export()
	if err != nil || len(exp.TaskTypes) == 0 {
		t.Fatalf("导出失败: %v", err)
	}
	var found bool
	for _, tt := range exp.TaskTypes {
		if tt.Type == model.TaskTypeRequirementImport {
			found = tt.WorkflowSource == MetadataSourceOverridden
		}
	}
	if !found {
		t.Fatal("导出应携带 workflow_source=overridden")
	}
}

func ptrSchema(s model.DatasetSchema) *model.DatasetSchema { return &s }
func ptrWorkflow(w model.Workflow) *model.Workflow         { return &w }

/* ---- Reload 边界 ---- */

func TestReloadSkipsIncompleteExtensionAnchor(t *testing.T) {
	svc, repo := newEditSvc(t)
	ctx := context.Background()
	// 只写锚行，不配套 schema/profile → 装载时整型缺席而非半成品
	payload, _ := json.Marshal(WorkflowPayload{DatasetType: "orphan_ds", Workflow: wizardWorkflow()})
	if err := repo.CreateEntry(ctx, &port.MetadataEntry{
		Kind: port.MetadataKindWorkflow, Key: "orphan_type", Version: 1,
		Payload: string(payload), Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := TaskTypeOf("orphan_type"); ok {
		t.Fatal("构件不全的锚行不应装载")
	}
	// 补齐 schema + profile 行后再装载即可见（同进程写后立即可读）
	sc := wizardSchema()
	sc.Type = "orphan_ds"
	if err := repo.CreateEntry(ctx, &port.MetadataEntry{
		Kind: port.MetadataKindDatasetSchema, Key: "orphan_ds", Version: 1,
		Payload: marshalJSON(sc), Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateEntry(ctx, &port.MetadataEntry{
		Kind: port.MetadataKindAnalyzeProfile, Key: "orphan_type", Version: 1,
		Payload: `{"role":"角色 {field_spec}"}`, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	d, ok := TaskTypeOf("orphan_type")
	if !ok || d.DatasetType != "orphan_ds" || d.Profile.Write.Schema.Type != "orphan_ds" {
		t.Fatalf("补齐后应装载: %v %+v", ok, d)
	}
	// 锚行禁用 → 回退离场（UpdateLatestEnabled 翻转）
	if err := repo.UpdateLatestEnabled(ctx, port.MetadataKindWorkflow, "orphan_type", false); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := TaskTypeOf("orphan_type"); ok {
		t.Fatal("锚行禁用后不应装载")
	}
}
