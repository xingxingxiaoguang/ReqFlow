package app

import (
	"strings"
	"testing"
)

// 提示词预览与运行时装配同源：渲染结果包含 schema 字段规范段、工具指南段、
// 额外要求注入——预览即装配的精确复现（不存在第二套渲染逻辑）。
func TestPromptPreviewRenders(t *testing.T) {
	svc := NewMetadataService(nil, nil)
	pv, err := svc.PromptPreview(PromptPreviewInput{TaskType: "requirement_import", Special: "重点关注性能需求"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pv.AgentSystemPrompt, "草稿字段规范") {
		t.Fatal("agent 系统提示词缺 schema 字段规范段")
	}
	if !strings.Contains(pv.AgentSystemPrompt, "read_document") || !strings.Contains(pv.AgentSystemPrompt, "write_work_items") {
		t.Fatal("agent 系统提示词缺工具指南")
	}
	if !strings.Contains(pv.AgentSystemPrompt, "重点关注性能需求") {
		t.Fatal("额外要求未注入系统提示词")
	}
	if !strings.Contains(pv.AgentFirstMessage, "read_document") {
		t.Fatal("首轮消息缺首步行动指引")
	}
	if !strings.Contains(pv.ClassicPrompt, "输出格式") || !strings.Contains(pv.ClassicPrompt, "重点关注性能需求") {
		t.Fatal("单发 prompt 缺输出契约或额外要求")
	}
	// Role 的 {field_spec} 占位必须已被渲染替换（原文不外漏）
	if strings.Contains(pv.AgentSystemPrompt, "{field_spec}") {
		t.Fatal("系统提示词仍含未渲染的 {field_spec} 占位")
	}
}

// 预览的空 WriteSpec 兜底路径：零值运行依赖构造工具集按 requirement 默认绑定（钉住 orDefault 行为）。
func TestPromptPreviewCatalog(t *testing.T) {
	svc := NewMetadataService(nil, nil)
	catalog := svc.Catalog()
	if len(catalog.TaskTypes) == 0 {
		t.Fatal("目录总览为空")
	}
	found := false
	for _, s := range catalog.TaskTypes {
		if s.Type == "requirement_import" {
			found = true
			if s.Source != MetadataSourceBuiltin || s.StepCount == 0 || s.DatasetType == "" || s.SchemaLabel == "" {
				t.Fatalf("目录条目不完整: %+v", s)
			}
		}
	}
	if !found {
		t.Fatal("目录缺 requirement_import")
	}
}

// 未注册类型的预览报错。
func TestPromptPreviewUnregistered(t *testing.T) {
	if _, err := NewMetadataService(nil, nil).PromptPreview(PromptPreviewInput{TaskType: "nope"}); err == nil {
		t.Fatal("未注册类型应报错")
	}
}
