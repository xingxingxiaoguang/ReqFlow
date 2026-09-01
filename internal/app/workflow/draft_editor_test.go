package workflow

import (
	"encoding/json"
	"testing"
	"time"

	domain "reqflow/internal/domain/workflow"
)

func TestInsertBetweenRewiresConnectionAtomically(t *testing.T) {
	catalog := editorCatalog(t)
	editor, err := NewDraftEditor(catalog)
	if err != nil {
		t.Fatal(err)
	}
	draft := editorDraft()
	original := draft.Connections[1]
	next, err := editor.InsertBetween(draft, InsertBetweenCommand{
		Connection: original,
		Node: domain.WorkflowNode{ID: "approve", Name: "人工确认",
			Capability: domain.CapabilityRef{Kind: "test.approve", Version: 1}, Config: json.RawMessage(`{}`)},
	})
	if err != nil {
		t.Fatalf("插入节点: %v", err)
	}
	if next.Revision != draft.Revision+1 {
		t.Fatalf("revision = %d, want %d", next.Revision, draft.Revision+1)
	}
	if containsConnection(next.Connections, original) {
		t.Fatal("原连接必须被切开")
	}
	order, err := domain.LinearOrder(next)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"source", "approve", "sink"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order[%d] = %s, want %s", i, order[i], want[i])
		}
	}
	if len(draft.Nodes) != 2 || !containsConnection(draft.Connections, original) {
		t.Fatal("编辑命令不能修改原草稿")
	}
}

func TestRemoveAndBridgeReconnectsNeighbors(t *testing.T) {
	catalog := editorCatalog(t)
	editor, _ := NewDraftEditor(catalog)
	draft := editorDraft()
	inserted, err := editor.InsertBetween(draft, InsertBetweenCommand{
		Connection: draft.Connections[1],
		Node: domain.WorkflowNode{ID: "approve", Name: "人工确认",
			Capability: domain.CapabilityRef{Kind: "test.approve", Version: 1}, Config: json.RawMessage(`{}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	next, err := editor.RemoveAndBridge(inserted, RemoveAndBridgeCommand{NodeID: "approve"})
	if err != nil {
		t.Fatalf("删除并桥接: %v", err)
	}
	order, err := domain.LinearOrder(next)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "source" || order[1] != "sink" {
		t.Fatalf("删除后的顺序非法: %v", order)
	}
	if !containsConnection(next.Connections, draft.Connections[1]) {
		t.Fatal("删除中间节点后应恢复原相邻连接")
	}
}

func TestInsertRejectsCapabilityThatCannotBridge(t *testing.T) {
	catalog := editorCatalog(t)
	editor, _ := NewDraftEditor(catalog)
	draft := editorDraft()
	_, err := editor.InsertBetween(draft, InsertBetweenCommand{
		Connection: draft.Connections[1],
		Node: domain.WorkflowNode{ID: "convert", Name: "转换",
			Capability: domain.CapabilityRef{Kind: "test.convert", Version: 1}, Config: json.RawMessage(`{}`)},
	})
	if err == nil {
		t.Fatal("不能桥接前后类型的节点必须在修改草稿前被拒绝")
	}
	if draft.Revision != 1 || len(draft.Nodes) != 2 {
		t.Fatal("失败的编辑命令不能修改原草稿")
	}
}

func TestAppendAndPrependRebindWorkflowBoundary(t *testing.T) {
	catalog := editorCatalog(t)
	editor, _ := NewDraftEditor(catalog)
	draft := editorDraft()
	next, err := editor.AppendAfter(draft, AppendAfterCommand{AfterNodeID: "sink", Node: domain.WorkflowNode{
		ID: "archive", Name: "归档", Capability: domain.CapabilityRef{Kind: "test.archive", Version: 1}, Config: json.RawMessage(`{}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if next.Outputs[0].ResourceType != "archive" {
		t.Fatalf("追加后流程输出类型 = %s", next.Outputs[0].ResourceType)
	}
	order, err := domain.LinearOrder(next)
	if err != nil || len(order) != 3 || order[2] != "archive" {
		t.Fatalf("追加后顺序非法: %v, %v", order, err)
	}

	next, err = editor.PrependBefore(next, PrependBeforeCommand{BeforeNodeID: "source", Node: domain.WorkflowNode{
		ID: "upload", Name: "上传", Capability: domain.CapabilityRef{Kind: "test.upload", Version: 1}, Config: json.RawMessage(`{}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if next.Inputs[0].ResourceType != "upload" {
		t.Fatalf("前置后流程输入类型 = %s", next.Inputs[0].ResourceType)
	}
	order, err = domain.LinearOrder(next)
	if err != nil || len(order) != 4 || order[0] != "upload" {
		t.Fatalf("前置后顺序非法: %v, %v", order, err)
	}
}

func TestReplaceAndConfigValidateAgainstCapabilitySchema(t *testing.T) {
	catalog := editorCatalog(t)
	editor, _ := NewDraftEditor(catalog)
	draft := editorDraft()
	next, err := editor.ReplaceNode(draft, ReplaceNodeCommand{Node: domain.WorkflowNode{
		ID: "source", Name: "新版来源", Capability: domain.CapabilityRef{Kind: "test.source_v2", Version: 1},
		Config: json.RawMessage(`{"mode":"safe"}`),
	}, InputPortMap: map[string]string{"in": "source"}, OutputPortMap: map[string]string{"out": "document"}})
	if err != nil {
		t.Fatal(err)
	}
	if next.Nodes[0].Capability.Kind != "test.source_v2" {
		t.Fatal("节点没有被替换")
	}
	if _, err := editor.SetNodeConfig(next, SetNodeConfigCommand{NodeID: "source", Config: json.RawMessage(`{"mode":"unsafe"}`)}); err == nil {
		t.Fatal("不符合 Capability Schema 的配置必须被拒绝")
	}
	if _, err := editor.SetNodeConfig(next, SetNodeConfigCommand{NodeID: "source", Config: json.RawMessage(`{"mode":"safe","extra":true}`)}); err == nil {
		t.Fatal("未声明的配置字段必须被拒绝")
	}
}

func TestRuleAndTopologyChangesInvalidateAcceptanceAndConfirmation(t *testing.T) {
	catalog := editorCatalog(t)
	editor, _ := NewDraftEditor(catalog)
	draft := editorDraft()
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	draft.AcceptanceCases = []domain.AcceptanceCase{{ID: "case_one", Name: "用例", Input: json.RawMessage(`{}`),
		Expectation: json.RawMessage(`{}`), LastPassed: true, LastPassedRevision: draft.Revision, LastPreviewID: "preview-1", LastRunAt: now}}
	draft.Rules.Decisions = []domain.RuleDecision{{Path: "rules.data_contract.record_granularity", Value: json.RawMessage(`"产品"`),
		Source: domain.DecisionUserConfirmed, Risk: domain.RiskHigh, Reason: "确认", ConfirmedBy: "user-1", ConfirmedAt: now}}
	next, err := editor.SetDataContract(draft, domain.DataContract{RecordGranularity: "产品版本", KeyFields: []string{"code"},
		Fields: []domain.FieldContract{{Key: "code", Label: "编码", Type: domain.FieldString, Required: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if next.AcceptanceCases[0].LastPassed || next.AcceptanceCases[0].LastPreviewID != "" {
		t.Fatal("规则变化必须使验收结果失效")
	}
	if next.Rules.Decisions[0].ConfirmedBy != "" || !next.Rules.Decisions[0].ConfirmedAt.IsZero() {
		t.Fatal("高风险规则变化必须清空旧确认")
	}
}

func editorCatalog(t *testing.T) *domain.StaticCatalog {
	t.Helper()
	definitions := []domain.CapabilityDefinition{
		testCapability("test.source", "raw", "document"),
		{Ref: domain.CapabilityRef{Kind: "test.source_v2", Version: 1}, Label: "新版来源", Description: "测试能力",
			Inputs:        []domain.PortDefinition{{Name: "source", Label: "输入", ResourceType: "raw", Role: domain.PortPrimary, Required: true}},
			Outputs:       []domain.PortDefinition{{Name: "document", Label: "输出", ResourceType: "document", Role: domain.PortPrimary, Required: true}},
			ConfigSchema:  json.RawMessage(`{"type":"object","properties":{"mode":{"type":"string","enum":["safe"]}},"required":["mode"],"additionalProperties":false}`),
			DefaultConfig: json.RawMessage(`{"mode":"safe"}`)},
		testCapability("test.approve", "document", "document"),
		testCapability("test.sink", "document", "artifact"),
		testCapability("test.convert", "document", "other"),
		testCapability("test.archive", "artifact", "archive"),
		testCapability("test.upload", "upload", "raw"),
	}
	catalog, err := domain.NewStaticCatalog(definitions...)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func testCapability(kind string, inputType, outputType domain.ResourceType) domain.CapabilityDefinition {
	return domain.CapabilityDefinition{
		Ref: domain.CapabilityRef{Kind: kind, Version: 1}, Label: kind, Description: "测试能力",
		Inputs: []domain.PortDefinition{{Name: "in", Label: "输入", ResourceType: inputType,
			Role: domain.PortPrimary, Required: true}},
		Outputs: []domain.PortDefinition{{Name: "out", Label: "输出", ResourceType: outputType,
			Role: domain.PortPrimary, Required: true}},
		ConfigSchema:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
		DefaultConfig: json.RawMessage(`{}`),
	}
}

func editorDraft() domain.WorkflowDraft {
	return domain.WorkflowDraft{
		ID: "draft-1", WorkspaceID: "default", Key: "editor_test", Name: "编辑测试", Revision: 1,
		Inputs:  []domain.WorkflowPort{{Name: "input", Label: "输入", ResourceType: "raw", Required: true}},
		Outputs: []domain.WorkflowPort{{Name: "result", Label: "结果", ResourceType: "artifact", Required: true}},
		Nodes: []domain.WorkflowNode{
			{ID: "source", Name: "来源", Capability: domain.CapabilityRef{Kind: "test.source", Version: 1}, Config: json.RawMessage(`{}`)},
			{ID: "sink", Name: "交付", Capability: domain.CapabilityRef{Kind: "test.sink", Version: 1}, Config: json.RawMessage(`{}`)},
		},
		Connections: []domain.Connection{
			{From: domain.Endpoint{Kind: domain.EndpointWorkflowInput, Port: "input"},
				To: domain.Endpoint{Kind: domain.EndpointNodeInput, NodeID: "source", Port: "in"}},
			{From: domain.Endpoint{Kind: domain.EndpointNodeOutput, NodeID: "source", Port: "out"},
				To: domain.Endpoint{Kind: domain.EndpointNodeInput, NodeID: "sink", Port: "in"}},
			{From: domain.Endpoint{Kind: domain.EndpointNodeOutput, NodeID: "sink", Port: "out"},
				To: domain.Endpoint{Kind: domain.EndpointWorkflowOutput, Port: "result"}},
		},
	}
}
