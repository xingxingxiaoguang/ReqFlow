package workflow_test

import (
	"encoding/json"
	"testing"
	"time"

	workflowapp "reqflow/internal/app/workflow"
	domain "reqflow/internal/domain/workflow"
)

func TestBuiltinCatalogRequiresManualFallbackForLLMCapabilities(t *testing.T) {
	_, err := domain.NewStaticCatalog(domain.CapabilityDefinition{
		Ref: domain.CapabilityRef{Kind: "ai.extract", Version: 1}, Label: "AI 抽取", Description: "测试",
		Inputs:      []domain.PortDefinition{{Name: "in", Label: "输入", ResourceType: "input", Role: domain.PortPrimary, Required: true}},
		Outputs:     []domain.PortDefinition{{Name: "out", Label: "输出", ResourceType: "output", Role: domain.PortPrimary, Required: true}},
		RequiresLLM: true,
	})
	if err == nil {
		t.Fatal("依赖 LLM 的 Capability 没有人工完成能力时必须拒绝注册")
	}

	catalog, err := workflowapp.BuiltinCatalog()
	if err != nil {
		t.Fatalf("注册内建 Capability: %v", err)
	}
	for _, definition := range catalog.Definitions() {
		if definition.RequiresLLM && !definition.ManualCompletion {
			t.Fatalf("Capability %s 违反手动降级不变量", definition.Ref.Kind)
		}
	}
}

func TestValidateAndBuildLinearRevision(t *testing.T) {
	catalog, err := workflowapp.BuiltinCatalog()
	if err != nil {
		t.Fatal(err)
	}
	draft := validDraft()
	issues := domain.Validate(draft, catalog, domain.ValidatePublish)
	if domain.HasErrors(issues) {
		t.Fatalf("有效线性流程不应有错误: %+v", issues)
	}

	publishedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	revision, err := domain.BuildRevision(draft, catalog, "revision-1", "user-1", publishedAt)
	if err != nil {
		t.Fatalf("构建 Revision: %v", err)
	}
	wantOrder := []string{"parse", "extract", "transform", "validate", "review", "publish", "index"}
	if len(revision.Nodes) != len(wantOrder) {
		t.Fatalf("节点数 = %d, want %d", len(revision.Nodes), len(wantOrder))
	}
	for i, want := range wantOrder {
		if revision.Nodes[i].ID != want {
			t.Fatalf("节点顺序[%d] = %s, want %s", i, revision.Nodes[i].ID, want)
		}
	}
	if len(revision.ContentHash) != 64 {
		t.Fatalf("content hash 非法: %q", revision.ContentHash)
	}
	if !revision.Nodes[1].Capability.RequiresLLM || !revision.Nodes[1].Capability.ManualCompletion {
		t.Fatal("发布快照必须固化 Capability 的 LLM 与人工完成合同")
	}
}

func TestValidateRejectsBranchAndTypeMismatch(t *testing.T) {
	catalog, err := workflowapp.BuiltinCatalog()
	if err != nil {
		t.Fatal(err)
	}
	draft := validDraft()
	draft.Nodes = append(draft.Nodes, domain.WorkflowNode{ID: "index_two", Name: "第二索引",
		Capability: domain.CapabilityRef{Kind: "retrieval.build", Version: 1}, Config: json.RawMessage(`{}`)})
	draft.Connections = append(draft.Connections,
		connection(nodeOutput("publish", "dataset"), nodeInput("index_two", "dataset")),
		connection(nodeOutput("index_two", "snapshot"), workflowOutput("snapshot_two")),
	)
	draft.Outputs = append(draft.Outputs, domain.WorkflowPort{Name: "snapshot_two", Label: "第二快照",
		ResourceType: domain.ResourceRetrievalSnapshot, Required: true})
	issues := domain.Validate(draft, catalog, domain.ValidatePublish)
	assertIssue(t, issues, "linear_branch_forbidden")

	draft = validDraft()
	draft.Connections[1] = connection(nodeOutput("parse", "documents"), nodeInput("transform", "drafts"))
	issues = domain.Validate(draft, catalog, domain.ValidatePublish)
	assertIssue(t, issues, "connection_type_mismatch")
}

func TestValidateRequiresConfirmedBusinessDecisions(t *testing.T) {
	catalog, err := workflowapp.BuiltinCatalog()
	if err != nil {
		t.Fatal(err)
	}
	draft := validDraft()
	draft.Rules.Decisions[0].ConfirmedBy = ""
	issues := domain.Validate(draft, catalog, domain.ValidatePublish)
	assertIssue(t, issues, "high_risk_decision_unconfirmed")
	assertIssue(t, issues, "business_decision_required")
}

func TestValidateRequiresUsedInputsAndInferenceEvidence(t *testing.T) {
	catalog, err := workflowapp.BuiltinCatalog()
	if err != nil {
		t.Fatal(err)
	}
	draft := validDraft()
	draft.Connections = append(draft.Connections[:4], draft.Connections[5:]...)
	draft.Rules.Decisions[0].Source = domain.DecisionInferred
	draft.Rules.Decisions[0].Evidence = nil

	issues := domain.Validate(draft, catalog, domain.ValidatePublish)
	assertIssue(t, issues, "workflow_input_unused")
	assertIssue(t, issues, "decision_evidence_required")
}

func TestDesignSessionFallsBackWithoutLosingDraft(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	manual, err := domain.NewDesignSession("session-manual", "draft-1", 7, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if manual.Mode != domain.AssistanceManual || manual.Status != domain.DesignManualEditing || !manual.CanEditManually() {
		t.Fatalf("无 Agent 时必须直接进入完整手动模式: %+v", manual)
	}
	if err := manual.StartAgent(now.Add(time.Second)); err == nil {
		t.Fatal("不可用的 Agent 不应启动")
	}

	session, err := domain.NewDesignSession("session-agent", "draft-1", 7, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AddProposal(domain.CommandProposal{ID: "proposal-1", DraftRevision: 7,
		Summary: "增加产品编码", Command: json.RawMessage(`{"type":"set_field"}`)}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.SwitchToManual(domain.ModelUnavailable, "provider timeout", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.DraftRevision != 7 || session.Mode != domain.AssistanceManual || session.Status != domain.DesignManualEditing {
		t.Fatalf("降级后必须保留草稿版本并进入手动模式: %+v", session)
	}
	if session.Proposals[0].Status != domain.ProposalObsolete {
		t.Fatalf("未接受的 Agent 建议必须失效，实际 %s", session.Proposals[0].Status)
	}
}

func TestDesignSessionCanSuspendForHumanDecision(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	session, err := domain.NewDesignSession("session-1", "draft-1", 2, true, now)
	if err != nil {
		t.Fatal(err)
	}
	err = session.RequestHuman(domain.HumanQuestion{ID: "granularity", Path: "rules.data_contract.record_granularity",
		Prompt: "一条记录代表什么？", Context: json.RawMessage(`{"candidates":["产品","产品版本"]}`)}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != domain.DesignAwaitingHuman {
		t.Fatalf("status = %s", session.Status)
	}
	if err := session.AnswerHuman(json.RawMessage(`"产品版本"`), "user-1", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.Status != domain.DesignAgentRunning || session.PendingQuestion.AnsweredAt.IsZero() ||
		session.PendingQuestion.AnsweredBy != "user-1" {
		t.Fatalf("回答后应恢复 Agent: %+v", session)
	}
}

func validDraft() domain.WorkflowDraft {
	confirmedAt := time.Date(2026, 9, 1, 11, 0, 0, 0, time.UTC)
	return domain.WorkflowDraft{
		ID: "workflow-1", WorkspaceID: "default", Key: "product_import", Name: "产品资料入库并索引", Revision: 3,
		Inputs: []domain.WorkflowPort{
			{Name: "assets", Label: "产品资料", ResourceType: domain.ResourceAssetSet, Required: true},
			{Name: "target", Label: "目标数据集", ResourceType: domain.ResourceDataset, Required: true},
		},
		Outputs: []domain.WorkflowPort{
			{Name: "batch", Label: "发布批次", ResourceType: domain.ResourceDatasetBatch, Required: true},
			{Name: "snapshot", Label: "检索快照", ResourceType: domain.ResourceRetrievalSnapshot, Required: true},
		},
		Nodes: []domain.WorkflowNode{
			{ID: "index", Name: "建立索引", Capability: domain.CapabilityRef{Kind: "retrieval.build", Version: 1}, Config: json.RawMessage(`{}`)},
			{ID: "validate", Name: "校验记录", Capability: domain.CapabilityRef{Kind: "data.validate", Version: 1}, Config: json.RawMessage(`{}`)},
			{ID: "parse", Name: "解析文件", Capability: domain.CapabilityRef{Kind: "source.parse", Version: 1}, Config: json.RawMessage(`{}`)},
			{ID: "publish", Name: "发布数据", Capability: domain.CapabilityRef{Kind: "data.publish", Version: 1}, Config: json.RawMessage(`{}`)},
			{ID: "extract", Name: "结构化抽取", Capability: domain.CapabilityRef{Kind: "document.extract", Version: 1}, Config: json.RawMessage(`{}`)},
			{ID: "review", Name: "人工审核", Capability: domain.CapabilityRef{Kind: "human.review_records", Version: 1}, Config: json.RawMessage(`{}`)},
			{ID: "transform", Name: "清洗记录", Capability: domain.CapabilityRef{Kind: "data.transform", Version: 1}, Config: json.RawMessage(`{}`)},
		},
		Connections: []domain.Connection{
			connection(workflowInput("assets"), nodeInput("parse", "assets")),
			connection(nodeOutput("parse", "documents"), nodeInput("extract", "documents")),
			connection(nodeOutput("extract", "drafts"), nodeInput("transform", "drafts")),
			connection(nodeOutput("transform", "records"), nodeInput("validate", "records")),
			connection(workflowInput("target"), nodeInput("validate", "dataset")),
			connection(nodeOutput("validate", "validation"), nodeInput("review", "validation")),
			connection(nodeOutput("review", "approved"), nodeInput("publish", "approved")),
			connection(nodeOutput("publish", "dataset"), nodeInput("index", "dataset")),
			connection(nodeOutput("publish", "batch"), workflowOutput("batch")),
			connection(nodeOutput("index", "snapshot"), workflowOutput("snapshot")),
		},
		Rules: domain.RuleBundle{
			DataContract: &domain.DataContract{RecordGranularity: "每个产品版本一条记录", KeyFields: []string{"product_code"},
				Fields: []domain.FieldContract{
					{Key: "product_code", Label: "产品编码", Type: domain.FieldString, Required: true},
					{Key: "name", Label: "产品名称", Type: domain.FieldString, Required: true},
					{Key: "description", Label: "产品说明", Type: domain.FieldString},
				}},
			Extraction: &domain.ExtractionSpec{Instruction: "只抽取原文有证据的值；缺失字段不得猜测。",
				FieldGuides: map[string]domain.FieldGuide{"product_code": {Description: "产品稳定编码", EvidenceOnly: true}}},
			Search: &domain.SearchSpec{Preset: domain.SearchBalanced,
				LexicalFields: []domain.WeightedField{{Field: "product_code", Weight: 3}, {Field: "name", Weight: 2}},
				VectorFields:  []string{"description"}, ChunkSize: 800, ChunkOverlap: 100,
				LexicalCandidates: 100, VectorCandidates: 100},
			Decisions: []domain.RuleDecision{
				{Path: "rules.data_contract.record_granularity", Value: json.RawMessage(`"每个产品版本一条记录"`),
					Source: domain.DecisionUserConfirmed, Risk: domain.RiskHigh, Confidence: 1,
					Reason: "业务确认版本是最小发布单位", ConfirmedBy: "user-1", ConfirmedAt: confirmedAt},
				{Path: "rules.data_contract.key_fields", Value: json.RawMessage(`["product_code"]`),
					Source: domain.DecisionUserConfirmed, Risk: domain.RiskHigh, Confidence: 1,
					Reason: "产品编码是业务唯一标识", ConfirmedBy: "user-1", ConfirmedAt: confirmedAt},
			},
		},
		AcceptanceCases: []domain.AcceptanceCase{{ID: "sample_product", Name: "产品样本",
			Input: json.RawMessage(`{"asset":"product-a.pdf"}`), Expectation: json.RawMessage(`{"records":1}`),
			LastPassed: true, LastPassedRevision: 3, LastPreviewID: "preview-1", LastRunAt: confirmedAt}},
	}
}

func workflowInput(port string) domain.Endpoint {
	return domain.Endpoint{Kind: domain.EndpointWorkflowInput, Port: port}
}

func workflowOutput(port string) domain.Endpoint {
	return domain.Endpoint{Kind: domain.EndpointWorkflowOutput, Port: port}
}

func nodeInput(nodeID, port string) domain.Endpoint {
	return domain.Endpoint{Kind: domain.EndpointNodeInput, NodeID: nodeID, Port: port}
}

func nodeOutput(nodeID, port string) domain.Endpoint {
	return domain.Endpoint{Kind: domain.EndpointNodeOutput, NodeID: nodeID, Port: port}
}

func connection(from, to domain.Endpoint) domain.Connection {
	return domain.Connection{From: from, To: to}
}

func assertIssue(t *testing.T, issues []domain.ValidationIssue, code string) {
	t.Helper()
	for _, issue := range issues {
		if issue.Code == code {
			return
		}
	}
	t.Fatalf("未找到 issue %s，实际: %+v", code, issues)
}
