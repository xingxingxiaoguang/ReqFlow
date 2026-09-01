package workflow

import (
	"encoding/json"
	"testing"

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

func editorCatalog(t *testing.T) *domain.StaticCatalog {
	t.Helper()
	definitions := []domain.CapabilityDefinition{
		testCapability("test.source", "raw", "document"),
		testCapability("test.approve", "document", "document"),
		testCapability("test.sink", "document", "artifact"),
		testCapability("test.convert", "document", "other"),
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
