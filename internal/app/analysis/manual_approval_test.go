package analysis

import (
	"context"
	"encoding/json"
	"testing"

	"reqflow/internal/domain/model"
	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

func approvalFixture(t *testing.T) (*analysisMemoryRepo, domain.RuleBundle, *model.AnalysisResult) {
	t.Helper()
	contract := domain.OutputContract{Fields: []domain.FieldContract{
		{Key: "summary", Label: "结论", Type: domain.FieldString, Required: true},
		{Key: "score", Label: "评分", Type: domain.FieldInteger, Required: true},
	}}
	schema, _, err := domain.CompileOutputContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	contractRaw, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	contractHash, err := domain.HashContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	source := &model.AnalysisResult{ID: "source-1", WorkspaceID: "default", Instruction: "生成结论",
		OutputContract: contractRaw, OutputContractHash: contractHash, OutputSchema: schema,
		Status: model.AnalysisResultSucceeded, Output: json.RawMessage(`{"summary":"初稿","score":60}`),
		Model: "test-model"}
	repo := &analysisMemoryRepo{result: source}
	return repo, domain.RuleBundle{OutputContract: &contract}, source
}

func approvalExecution(payload string) port.WorkflowManualExecution {
	return port.WorkflowManualExecution{WorkspaceID: "default", RunID: "run-1",
		NodeRunID: "approve-node-1", Attempt: 1, Actor: "approver-1", Payload: json.RawMessage(payload),
		Inputs: []domain.NodeResourceBinding{{Port: "analysis", Direction: domain.BindingInput,
			ResourceType: domain.ResourceAnalysisResult, ResourceID: "source-1"}}}
}

func TestApproveAnalysisCreatesHumanProducedResult(t *testing.T) {
	repo, rules, _ := approvalFixture(t)
	completer, err := NewWorkflowApproveAnalysisManualCompleter(repo)
	if err != nil {
		t.Fatal(err)
	}
	execution := approvalExecution(`{"decision":"edit","output":{"summary":"修订结论","score":95},"rationale":"修正评分"}`)
	execution.Rules = rules
	outputs, err := completer.Complete(context.Background(), execution)
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 1 || outputs[0].Port != "approved" ||
		outputs[0].ResourceType != domain.ResourceAnalysisResult || outputs[0].ResourceID == "" ||
		outputs[0].ResourceID == "source-1" {
		t.Fatalf("必须返回人工 NodeRun 生产的新 AnalysisResult: %+v", outputs)
	}
	human := repo.result
	if human.ID == "source-1" || human.Status != model.AnalysisResultSucceeded ||
		human.ProducerNodeRunID != "approve-node-1" || human.Model != "human" {
		t.Fatalf("人工确认结果应替换内存中的正式结果: %+v", human)
	}
	if string(human.Output) != `{"score":95,"summary":"修订结论"}` {
		t.Fatalf("编辑输出应按 Schema 规范序列化: %s", human.Output)
	}
	if human.AgentContext == nil || human.OutputContractHash == "" {
		t.Fatalf("人工结果必须携带 agent_context 与合同哈希: %+v", human)
	}
}

func TestApproveAnalysisRejectsContractViolations(t *testing.T) {
	repo, rules, source := approvalFixture(t)
	completer, _ := NewWorkflowApproveAnalysisManualCompleter(repo)
	// 合同不一致：Revision 的 OutputContract 与来源结果不同。
	tampered := domain.RuleBundle{OutputContract: &domain.OutputContract{Fields: []domain.FieldContract{
		{Key: "other", Label: "其它", Type: domain.FieldString, Required: true}}}}
	execution := approvalExecution(`{"decision":"approve","rationale":"确认"}`)
	execution.Rules = tampered
	if _, err := completer.Complete(context.Background(), execution); err == nil {
		t.Fatal("来源结果与 Revision 合同不一致时必须拒绝")
	}
	// 输出违反 Schema：编辑提交缺失必填字段。
	execution = approvalExecution(`{"decision":"edit","output":{"summary":"缺评分"},"rationale":"确认"}`)
	execution.Rules = rules
	if _, err := completer.Complete(context.Background(), execution); err == nil {
		t.Fatal("编辑输出必须通过 OutputContract 校验")
	}
	// 来源结果未完成时不允许确认。
	source.Status = model.AnalysisResultRunning
	repo.result = source
	execution = approvalExecution(`{"decision":"approve","rationale":"确认"}`)
	execution.Rules = rules
	if _, err := completer.Complete(context.Background(), execution); err == nil {
		t.Fatal("只有 succeeded AnalysisResult 允许人工确认")
	}
}
