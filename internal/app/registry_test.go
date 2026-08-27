package app

import (
	"testing"

	"reqflow/internal/domain/model"
)

// 聚合注册表与旧查找入口的一致性：薄委托不得漂移（M1 验收项——
// WorkflowOf / AnalyzeProfileOf 委托注册表后，存量调用方行为不变）。
func TestRegistryDelegationConsistency(t *testing.T) {
	defs := TaskTypes()
	if len(defs) == 0 {
		t.Fatal("聚合注册表为空")
	}
	if got := len(Workflows()); got != len(defs) {
		t.Fatalf("Workflows() 条数 %d != 注册表 %d", got, len(defs))
	}
	for _, d := range defs {
		w, ok := WorkflowOf(d.Type)
		if !ok || w.Type != d.Workflow.Type || len(w.Steps) != len(d.Workflow.Steps) {
			t.Fatalf("WorkflowOf(%s) 与注册表不一致", d.Type)
		}
		p, ok := AnalyzeProfileOf(d.Type)
		if !ok || p.Role != d.Profile.Role || p.Example != d.Profile.Example {
			t.Fatalf("AnalyzeProfileOf(%s) 与注册表不一致", d.Type)
		}
		// 聚合声明的产出数据集类型与域层映射互相钉住（两处来源不漂移）
		dt, ok := model.DatasetTypeOfTask(d.Type)
		if !ok || dt != d.DatasetType {
			t.Fatalf("注册表 %s 的 DatasetType(%s) 与 model.DatasetTypeOfTask(%v) 不一致", d.Type, d.DatasetType, dt)
		}
		if d.Schema == nil || d.Schema().Type != d.DatasetType {
			t.Fatalf("注册表 %s 的 schema 类型与 DatasetType 不一致", d.Type)
		}
	}
	if _, ok := TaskTypeOf("no_such_type"); ok {
		t.Fatal("未注册类型应返回 false")
	}
}

// 聚合视图：schema/profile/工具清单完整，写入绑定带出工具名。
func TestMetadataTaskTypeView(t *testing.T) {
	view, err := NewMetadataService(nil, nil).TaskTypeView("requirement_import", false)
	if err != nil {
		t.Fatal(err)
	}
	if view.Source != MetadataSourceBuiltin {
		t.Fatalf("M1 source 应为 builtin，得到 %s", view.Source)
	}
	if view.Schema.Type != view.DatasetType || len(view.Schema.Fields) == 0 {
		t.Fatalf("聚合视图 schema(%s) 与产出类型(%s) 不一致或字段为空", view.Schema.Type, view.DatasetType)
	}
	if view.Profile.Write.ToolName != "write_work_items" {
		t.Fatalf("写入绑定工具名 = %q", view.Profile.Write.ToolName)
	}
	if view.Profile.Role == "" || len(view.Workflow.Steps) == 0 {
		t.Fatal("聚合视图缺 Role 或步骤链")
	}
	names := map[string]bool{}
	for _, tool := range view.Tools {
		names[tool.Name] = true
		if tool.Snippet == "" || len(tool.Guidelines) == 0 {
			t.Fatalf("工具 %s 缺提示词素材（snippet/guidelines）", tool.Name)
		}
	}
	for _, want := range []string{"read_document", "search_document", "write_work_items", "ask_human"} {
		if !names[want] {
			t.Fatalf("工具清单缺 %s", want)
		}
	}
}

// 未注册类型的聚合视图报错。
func TestMetadataTaskTypeViewUnregistered(t *testing.T) {
	if _, err := NewMetadataService(nil, nil).TaskTypeView("nope", false); err == nil {
		t.Fatal("未注册类型应报错")
	}
}
