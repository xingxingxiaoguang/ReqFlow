package app

// M3 写路径测试：seed→override→effective 生效、版本递增、审计落库、
// 守卫拦截（❌/⚠️）、enabled=false 回退 seed、写后立即读（缓存失效）、
// 快照隔离（schema 快照优先于 effective）。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

/* ---- 假元数据仓储 ---- */

type memMetadata struct {
	mu      sync.Mutex
	entries []port.MetadataEntry
	audits  []port.MetadataAuditEntry
}

func (m *memMetadata) CreateEntry(_ context.Context, e *port.MetadataEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e.CreatedAt = time.Now()
	m.entries = append(m.entries, *e)
	return nil
}

func (m *memMetadata) LatestEntries(_ context.Context) ([]port.MetadataEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	latest := map[string]port.MetadataEntry{}
	for _, e := range m.entries {
		k := e.Kind + "\x1f" + e.Key
		if cur, ok := latest[k]; !ok || e.Version > cur.Version {
			latest[k] = e
		}
	}
	out := make([]port.MetadataEntry, 0, len(latest))
	for _, e := range latest {
		out = append(out, e)
	}
	return out, nil
}

func (m *memMetadata) ListVersions(_ context.Context, kind, key string, _ int) ([]port.MetadataEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []port.MetadataEntry
	for _, e := range m.entries {
		if e.Kind == kind && e.Key == key {
			out = append(out, e)
		}
	}
	// 新→旧（简单插入排序，条目少）
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Version > out[j-1].Version; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

func (m *memMetadata) WriteAudit(_ context.Context, a *port.MetadataAuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a.CreatedAt = time.Now()
	m.audits = append(m.audits, *a)
	return nil
}

func (m *memMetadata) ListAudit(_ context.Context, kind, key string, _ int) ([]port.MetadataAuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []port.MetadataAuditEntry
	for _, a := range m.audits {
		if a.Kind == kind && a.Key == key {
			out = append(out, a)
		}
	}
	return out, nil
}

/* ---- 测试基建 ---- */

// newEditSvc 构造带假仓储的 MetadataService；测试后清空 override 合并层（进程内全局态）。
func newEditSvc(t *testing.T) (*MetadataService, *memMetadata) {
	t.Helper()
	repo := &memMetadata{}
	svc := NewMetadataService(repo, nil)
	t.Cleanup(func() { setMetadataOverrides(nil, nil) })
	return svc, repo
}

func requirementSeed(t *testing.T) model.DatasetSchema {
	t.Helper()
	s, ok := seedSchemaOf(model.DatasetTypeRequirement)
	if !ok {
		t.Fatal("requirement seed 未注册")
	}
	return s
}

/* ---- schema 写路径 ---- */

func TestUpdateSchemaCompatibleChangeTakesEffectImmediately(t *testing.T) {
	svc, repo := newEditSvc(t)
	next := requirementSeed(t)
	next.Fields[4].Prompt = "新的优先级提取说明" // priority：纯文案变更（兼容）
	next.Fields[4].Enum = []string{"High", "Medium", "Low", "Urgent"} // 枚举扩值（兼容）

	res, err := svc.UpdateSchema(context.Background(), SchemaUpdateInput{
		DatasetType: model.DatasetTypeRequirement, Schema: next, Summary: "扩值+文案",
	})
	if err != nil || !res.Saved {
		t.Fatalf("兼容变更应保存成功: err=%v res=%+v", err, res)
	}
	if res.Source != MetadataSourceOverridden || res.Version != 2 { // seed v1 + 注册表 v1
		t.Fatalf("保存后 source/version 应为 overridden/v2: %+v", res)
	}
	// 写后立即读：effective 已生效（缓存失效验证——M3 风险表第一条）
	d, ok := TaskTypeOf(model.TaskTypeRequirementImport)
	if !ok {
		t.Fatal("requirement_import 未注册")
	}
	eff := d.Schema()
	if eff.Version != 2 || !schemaOverridden(model.DatasetTypeRequirement) {
		t.Fatalf("effective 未刷新: v%d overridden=%v", eff.Version, schemaOverridden(model.DatasetTypeRequirement))
	}
	found := false
	for _, f := range eff.Fields {
		if f.Key == "priority" && f.Prompt == "新的优先级提取说明" && len(f.Enum) == 4 {
			found = true
		}
	}
	if !found {
		t.Fatal("effective schema 未体现变更")
	}
	// 校验/归一化同口径跟随（写入工具绑定同一 schema）
	if d.Profile.Write.Schema.Version != 2 {
		t.Fatalf("写入绑定未跟随 effective: v%d", d.Profile.Write.Schema.Version)
	}
	// 审计落库
	audits, _ := repo.ListAudit(context.Background(), "dataset_schema", model.DatasetTypeRequirement, 10)
	if len(audits) != 1 || audits[0].Action != "update_schema" || audits[0].FromVersion != 1 || audits[0].ToVersion != 2 {
		t.Fatalf("审计不符: %+v", audits)
	}
}

func TestUpdateSchemaEnumNarrowBlocked(t *testing.T) {
	svc, repo := newEditSvc(t)
	next := requirementSeed(t)
	next.Fields[4].Enum = []string{"High", "Medium"} // 枚举收窄

	res, err := svc.UpdateSchema(context.Background(), SchemaUpdateInput{
		DatasetType: model.DatasetTypeRequirement, Schema: next,
	})
	if err == nil || res == nil || res.Saved {
		t.Fatal("枚举收窄应被拦截")
	}
	if !res.Report.Blocked {
		t.Fatalf("判定应为 ❌: %+v", res.Report)
	}
	// 未落库、effective 不变
	if _, ok := TaskTypeOf(model.TaskTypeRequirementImport); !ok {
		t.Fatal("注册表损坏")
	}
	if schemaOverridden(model.DatasetTypeRequirement) {
		t.Fatal("被拦截的变更不应生效")
	}
	entries, _ := repo.ListVersions(context.Background(), "dataset_schema", model.DatasetTypeRequirement, 10)
	if len(entries) != 0 {
		t.Fatal("被拦截的变更不应落库")
	}
}

func TestUpdateSchemaRemovedFieldBlocked(t *testing.T) {
	svc, _ := newEditSvc(t)
	next := requirementSeed(t)
	next.Fields = next.Fields[:len(next.Fields)-1]
	res, err := svc.UpdateSchema(context.Background(), SchemaUpdateInput{
		DatasetType: model.DatasetTypeRequirement, Schema: next,
	})
	if err == nil || res.Saved || !res.Report.Blocked {
		t.Fatalf("删字段应 ❌ 拦截: err=%v", err)
	}
}

func TestUpdateSchemaWarnNeedsConfirm(t *testing.T) {
	svc, _ := newEditSvc(t)
	next := requirementSeed(t)
	next.Fields = append(next.Fields, model.FieldSpec{Key: "extra", Label: "附加", Type: model.FieldString, Required: true})

	res, err := svc.UpdateSchema(context.Background(), SchemaUpdateInput{
		DatasetType: model.DatasetTypeRequirement, Schema: next,
	})
	if err == nil || res.Saved {
		t.Fatal("⚠️ 项未确认应被拦")
	}
	res2, err2 := svc.UpdateSchema(context.Background(), SchemaUpdateInput{
		DatasetType: model.DatasetTypeRequirement, Schema: next, ConfirmRisky: true, Summary: "确认新增必填",
	})
	if err2 != nil || !res2.Saved {
		t.Fatalf("确认后应放行: %v", err2)
	}
	// 审计记录带确认说明
	eff, _ := effectiveSchemaOf(model.DatasetTypeRequirement)
	if eff.Version != 2 {
		t.Fatalf("确认保存后 effective v=%d", eff.Version)
	}
}

func TestUpdateSchemaInVectorChangeMarksReembed(t *testing.T) {
	svc, _ := newEditSvc(t)
	next := requirementSeed(t)
	next.Fields[2].InVector = model.VectorNone // description 退出向量
	res, err := svc.UpdateSchema(context.Background(), SchemaUpdateInput{
		DatasetType: model.DatasetTypeRequirement, Schema: next, ConfirmRisky: true,
	})
	if err != nil || !res.Saved {
		t.Fatalf("InVector 变更确认后应保存: %v", err)
	}
	if !res.Report.NeedsReembed {
		t.Fatal("InVector 变更应标记需重嵌")
	}
}

func TestUpdateSchemaShapeValidation(t *testing.T) {
	svc, _ := newEditSvc(t)
	next := requirementSeed(t)
	next.Fields[0].Key = "Title-Bad"
	if _, err := svc.UpdateSchema(context.Background(), SchemaUpdateInput{
		DatasetType: model.DatasetTypeRequirement, Schema: next,
	}); err == nil || !strings.Contains(err.Error(), "key") {
		t.Fatalf("非法 key 应被形状校验拦截: %v", err)
	}
	if _, err := svc.UpdateSchema(context.Background(), SchemaUpdateInput{
		DatasetType: "no_such_dataset_type", Schema: requirementSeed(t),
	}); err == nil {
		t.Fatal("未注册数据集类型应报错")
	}
}

/* ---- 版本递增 / 回退 / 禁用 ---- */

func TestSchemaVersionIncrementAndReset(t *testing.T) {
	svc, repo := newEditSvc(t)
	ctx := context.Background()

	save := func(prompt string) *SchemaUpdateResult {
		next := requirementSeed(t)
		next.Fields[4].Prompt = prompt
		res, err := svc.UpdateSchema(ctx, SchemaUpdateInput{DatasetType: model.DatasetTypeRequirement, Schema: next})
		if err != nil {
			t.Fatalf("保存失败: %v", err)
		}
		return res
	}
	if v := save("v2 说明").Version; v != 2 {
		t.Fatalf("第一版 effective 应为 v2: %d", v)
	}
	if v := save("v3 说明").Version; v != 3 {
		t.Fatalf("第二版 effective 应为 v3: %d", v)
	}

	// 版本历史保留（两版 + reset 版）
	history, err := svc.History(ctx, "dataset_schema", model.DatasetTypeRequirement)
	if err != nil || len(history) != 2 {
		t.Fatalf("版本历史不符: n=%d err=%v", len(history), err)
	}

	// 回退到内置：effective 回 seed，历史仍保留
	res, err := svc.ResetSchema(ctx, model.DatasetTypeRequirement)
	if err != nil || !res.Saved || res.Source != MetadataSourceBuiltin {
		t.Fatalf("回退失败: %v %+v", err, res)
	}
	eff, _ := effectiveSchemaOf(model.DatasetTypeRequirement)
	if eff.Version != requirementSeed(t).Version || schemaOverridden(model.DatasetTypeRequirement) {
		t.Fatalf("回退后应回到 seed: v%d", eff.Version)
	}
	history2, _ := svc.History(ctx, "dataset_schema", model.DatasetTypeRequirement)
	if len(history2) != 3 || history2[0].Enabled {
		t.Fatalf("回退版应为最新且 disabled: %+v", history2[0])
	}
	// 再回退（已是 builtin）幂等
	if res2, err := svc.ResetSchema(ctx, model.DatasetTypeRequirement); err != nil || res2.Saved {
		t.Fatalf("重复回退应幂等: %v %+v", err, res2)
	}
	_ = repo
}

func TestDisabledLatestFallsBackToSeed(t *testing.T) {
	// 直接经假仓储写入 disabled 最新版 → Reload 后回退 seed（enabled=false 语义钉住）
	svc, repo := newEditSvc(t)
	seed := requirementSeed(t)
	next := seed
	next.Fields[4].Prompt = "被禁用的版本"
	if err := repo.CreateEntry(context.Background(), &port.MetadataEntry{
		Kind: "dataset_schema", Key: model.DatasetTypeRequirement, Version: 1,
		Payload: marshalJSON(next), Enabled: false, Summary: "x",
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if schemaOverridden(model.DatasetTypeRequirement) {
		t.Fatal("disabled 最新版应回退 seed")
	}
}

/* ---- profile 写路径 ---- */

func TestUpdateProfileEffectiveAndReset(t *testing.T) {
	svc, _ := newEditSvc(t)
	ctx := context.Background()
	d, _ := TaskTypeOf(model.TaskTypeRequirementImport)

	res, err := svc.UpdateProfile(ctx, ProfileUpdateInput{
		TaskType: model.TaskTypeRequirementImport,
		Role:     strings.Replace(d.Profile.Role, "专业的项目管理助手", "资深的需求架构师", 1),
		Example:  d.Profile.Example,
		Summary:  "调整指令头",
	})
	if err != nil || !res.Saved || res.Source != MetadataSourceOverridden {
		t.Fatalf("profile 保存失败: %v %+v", err, res)
	}
	// 写后立即读 + 提示词预览即时体现（验收项）
	p2, _ := TaskTypeOf(model.TaskTypeRequirementImport)
	if !strings.Contains(p2.Profile.Role, "资深的需求架构师") {
		t.Fatal("effective profile 未生效")
	}
	view, err := NewMetadataService(nil, nil).PromptPreview(PromptPreviewInput{TaskType: model.TaskTypeRequirementImport})
	if err != nil || !strings.Contains(view.AgentSystemPrompt, "资深的需求架构师") {
		t.Fatalf("提示词预览应即时反映 profile 覆盖: %v", err)
	}
	// 整体 source 徽标
	if !profileOverridden(model.TaskTypeRequirementImport) {
		t.Fatal("profileOverridden 判定失效")
	}

	// {field_spec} 丢失告警
	res2, err := svc.UpdateProfile(ctx, ProfileUpdateInput{
		TaskType: model.TaskTypeRequirementImport,
		Role:     strings.ReplaceAll(p2.Profile.Role, "{field_spec}", "字段规范见下"),
		Example:  p2.Profile.Example,
	})
	if err != nil || !res2.Saved {
		t.Fatalf("告警项不拦截: %v", err)
	}
	warned := false
	for _, f := range res2.Findings {
		if f.Rule == "field_spec_lost" && f.Level == "warn" {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("应含 field_spec_lost 告警: %+v", res2.Findings)
	}

	// 回退
	if res3, err := svc.ResetProfile(ctx, model.TaskTypeRequirementImport); err != nil || !res3.Saved {
		t.Fatalf("profile 回退失败: %v", err)
	}
	if profileOverridden(model.TaskTypeRequirementImport) {
		t.Fatal("回退后应回到 seed")
	}
}

/* ---- 快照隔离 ---- */

func TestProfileWithSnapshotIsolatesInflightTasks(t *testing.T) {
	svc, _ := newEditSvc(t)
	ctx := context.Background()

	// 任务启动时的 schema（会话快照）
	snap := requirementSeed(t)
	snap.Fields[4].Enum = []string{"High", "Medium", "Low"}
	cc := &port.Context{TaskSchema: marshalJSON(snap)}

	// 任务进行中：元数据被编辑（priority 收窄 + prompt 改写——收窄本会被守卫拦截，
	// 这里经假仓储直写模拟绕过守卫的场景，验证重放口径仍以快照为准）
	narrowed := requirementSeed(t)
	narrowed.Fields[4].Enum = []string{"High"}
	narrowed.Fields[4].Prompt = "编辑后的说明"
	if err := svc.repo.CreateEntry(ctx, &port.MetadataEntry{
		Kind: "dataset_schema", Key: model.DatasetTypeRequirement, Version: 1,
		Payload: marshalJSON(narrowed), Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(ctx); err != nil {
		t.Fatal(err)
	}

	profile, err := profileFor(model.TaskTypeRequirementImport)
	if err != nil {
		t.Fatal(err)
	}
	isolated := profileWithSnapshot(cc, profile)
	// 会话重放按快照口径：旧枚举仍合法
	if got := isolated.Schema().Fields[4].Enum; len(got) != 3 {
		t.Fatalf("重放应按快照枚举（3 值），得到 %v", got)
	}
	if isolated.Write.Schema.Fields[4].Prompt != snap.Fields[4].Prompt {
		t.Fatal("写入绑定应按快照")
	}
	// 新任务（无快照）按 effective
	if got := profile.Schema().Fields[4].Enum; len(got) != 1 {
		t.Fatalf("新任务应按 effective（收窄后 1 值），得到 %v", got)
	}
	// 坏快照回退 effective
	bad := profileWithSnapshot(&port.Context{TaskSchema: "{bad json"}, profile)
	if bad.Schema().Fields[4].Prompt != "编辑后的说明" {
		t.Fatal("坏快照应回退 effective")
	}
}

/* ---- 导出 / 导入 / 历史 ---- */

func TestExportReflectsEffective(t *testing.T) {
	svc, _ := newEditSvc(t)
	next := requirementSeed(t)
	next.Fields[4].Prompt = "导出验证"
	if _, err := svc.UpdateSchema(context.Background(), SchemaUpdateInput{
		DatasetType: model.DatasetTypeRequirement, Schema: next,
	}); err != nil {
		t.Fatal(err)
	}
	doc, err := svc.Export()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tt := range doc.TaskTypes {
		if tt.Type == model.TaskTypeRequirementImport && tt.SchemaSource == MetadataSourceOverridden {
			for _, f := range tt.Schema.Fields {
				if f.Key == "priority" && f.Prompt == "导出验证" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("导出应含 effective 覆盖定义")
	}
}

func TestImportRunsSameGuards(t *testing.T) {
	svc, _ := newEditSvc(t)
	narrowed := requirementSeed(t)
	narrowed.Fields[4].Enum = []string{"High"} // ❌ 项
	okSchema := requirementSeed(t)
	okSchema.Fields[4].Prompt = "导入的兼容变更"

	res, err := svc.Import(context.Background(), MetadataImportInput{
		TaskTypes: []MetadataImportItem{
			{Type: model.TaskTypeRequirementImport, Schema: &narrowed}, // 被拦截
			{Type: model.TaskTypeRequirementImport, Schema: &okSchema}, // 落库
			{Type: "no_such", Schema: &okSchema},                        // 未注册
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 {
		t.Fatalf("导入结果应逐项返回: %+v", res.Items)
	}
	if res.Items[0].Error == "" || res.Items[0].Schema.Saved {
		t.Fatalf("枚举收窄项应被拦截: %+v", res.Items[0])
	}
	if res.Items[1].Error != "" || !res.Items[1].Schema.Saved {
		t.Fatalf("兼容项应落库: %+v", res.Items[1])
	}
	if res.Items[2].Error == "" {
		t.Fatal("未注册类型应报错")
	}
}

func TestHistoryPayloadRoundTrip(t *testing.T) {
	svc, _ := newEditSvc(t)
	next := requirementSeed(t)
	next.Fields[4].Prompt = "历史回放"
	if _, err := svc.UpdateSchema(context.Background(), SchemaUpdateInput{
		DatasetType: model.DatasetTypeRequirement, Schema: next,
	}); err != nil {
		t.Fatal(err)
	}
	history, err := svc.History(context.Background(), "dataset_schema", model.DatasetTypeRequirement)
	if err != nil || len(history) != 1 {
		t.Fatalf("历史查询失败: %v", err)
	}
	var stored model.DatasetSchema
	if err := json.Unmarshal(history[0].Payload, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Type != model.DatasetTypeRequirement || stored.Version != 2 || stored.Fields[4].Prompt != "历史回放" {
		t.Fatalf("载荷应可往返解析（version=有效版本）: %+v", stored.Fields[4])
	}
	// 非法 kind 拒绝
	if _, err := svc.History(context.Background(), "workflow", "x"); err == nil {
		t.Fatal("M3 不支持的 kind 应报错")
	}
}

func TestRepoNilGuards(t *testing.T) {
	svc := NewMetadataService(nil, nil)
	if _, err := svc.UpdateSchema(context.Background(), SchemaUpdateInput{DatasetType: model.DatasetTypeRequirement}); err == nil {
		t.Fatal("无仓储应报错")
	}
	if err := svc.Reload(context.Background()); err != nil {
		t.Fatal("无仓储 Reload 应为空操作")
	}
	// memDatasets 影响面路径（ListDatasets 可用）
	mds := newMemDatasets()
	if err := mds.CreateDataset(context.Background(), &model.Dataset{
		Type: model.DatasetTypeRequirement, Name: "存量集", ItemCount: 7, SchemaVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	svc2 := NewMetadataService(&memMetadata{}, mds)
	reseed := requirementSeed(t)
	reseed.Fields[2].InVector = model.VectorNone
	res, err := svc2.CheckSchema(context.Background(), SchemaUpdateInput{
		DatasetType: model.DatasetTypeRequirement, Schema: reseed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Datasets) != 1 || !res.Datasets[0].NeedsReembed || res.Datasets[0].ItemCount != 7 {
		t.Fatalf("影响面清单不符: %+v", res.Datasets)
	}
	if res.Saved {
		t.Fatal("check 不应落库")
	}
	_ = fmt.Sprintf("%v", res)
}
