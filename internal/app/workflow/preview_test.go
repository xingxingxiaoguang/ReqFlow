package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type stubDryRunner struct {
	ref domain.CapabilityRef
	run func(execution port.WorkflowDryRunExecution) (port.WorkflowDryRunResult, error)
}

func (r stubDryRunner) Capability() domain.CapabilityRef { return r.ref }
func (r stubDryRunner) DryRun(_ context.Context, execution port.WorkflowDryRunExecution) (port.WorkflowDryRunResult, error) {
	return r.run(execution)
}

func requireWorkflowInput(execution port.WorkflowDryRunExecution, portName string) (string, error) {
	for _, input := range execution.Inputs {
		if input.Port == portName && input.Direction == domain.BindingInput {
			if strings.TrimSpace(input.ResourceID) == "" {
				return "", portDryRunError("输入 " + portName + " 缺少 resource_id")
			}
			return input.ResourceID, nil
		}
	}
	return "", portDryRunError("输入 " + portName + " 未绑定")
}

type portDryRunError string

func (e portDryRunError) Error() string { return string(e) }

// editorDryRuns 构造与 editorCatalog 配套的 stub dry-run：source 要求流程
// 输入资源，sink 要求上游样本并模拟交付一份制品。
func editorDryRuns(t *testing.T) *DryRunRegistry {
	t.Helper()
	registry, err := NewDryRunRegistry(
		stubDryRunner{ref: domain.CapabilityRef{Kind: "test.source", Version: 1},
			run: func(execution port.WorkflowDryRunExecution) (port.WorkflowDryRunResult, error) {
				if _, err := requireWorkflowInput(execution, "in"); err != nil {
					return port.WorkflowDryRunResult{}, err
				}
				return port.WorkflowDryRunResult{
					Outputs: []domain.NodeResourceBinding{{Port: "out", ResourceType: "document",
						ResourceID: domain.TemporaryResourceID(execution.PreviewID, execution.Node.ID, "out")}},
					Samples: map[string]json.RawMessage{"out": json.RawMessage(`{"value":"raw"}`)},
					Metrics: map[string]any{"records": 1},
				}, nil
			}},
		stubDryRunner{ref: domain.CapabilityRef{Kind: "test.sink", Version: 1},
			run: func(execution port.WorkflowDryRunExecution) (port.WorkflowDryRunResult, error) {
				upstream, ok := execution.Samples.Upstream("in")
				if !ok {
					return port.WorkflowDryRunResult{}, portDryRunError("缺少上游 document 样本")
				}
				if upstream.NodeID != "source" || string(upstream.Payload) != `{"value":"raw"}` {
					return port.WorkflowDryRunResult{}, portDryRunError("上游样本内容不匹配")
				}
				return port.WorkflowDryRunResult{
					Outputs: []domain.NodeResourceBinding{{Port: "out", ResourceType: "artifact",
						ResourceID: domain.TemporaryResourceID(execution.PreviewID, execution.Node.ID, "out")}},
					Samples:   map[string]json.RawMessage{"out": json.RawMessage(`{"artifact":"模拟"}`)},
					Simulated: true,
					Metrics:   map[string]any{"items": 1},
				}, nil
			}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func decodeManifest(t *testing.T, raw json.RawMessage) actualRoot {
	t.Helper()
	var manifest actualRoot
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("manifest 非法: %v", err)
	}
	return manifest
}

func TestPreviewRunsChainWithTemporaryOutputs(t *testing.T) {
	catalog := editorCatalog(t)
	repo := &memoryWorkflowRepo{draft: editorDraft()}
	service, err := NewPreviewService(repo, catalog, editorDryRuns(t))
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.Create(context.Background(), repo.draft.ID, CreatePreviewRequest{
		Input: json.RawMessage(`{"inputs":{"input":{"resource_id":"raw-1"}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Status != domain.PreviewPassed || !preview.Temporary {
		t.Fatalf("预览应通过且 temporary: %+v", preview)
	}
	manifest := decodeManifest(t, preview.OutputManifest)
	if !manifest.Temporary {
		t.Fatal("manifest 必须整体标记 temporary")
	}
	source, ok := manifest.Nodes["source"]
	if !ok || source.Status != "succeeded" || source.Simulated {
		t.Fatalf("source 节点结果非法: %+v", source)
	}
	sink := manifest.Nodes["sink"]
	if sink.Status != "succeeded" || !sink.Simulated {
		t.Fatalf("sink 节点应为标记模拟的成功: %+v", sink)
	}
	output := sink.Outputs["out"]
	if !output.Temporary || output.ResourceType != domain.ResourceArtifact ||
		!strings.HasPrefix(output.ResourceID, "preview:") {
		t.Fatalf("sink 输出必须是 temporary 绑定: %+v", output)
	}
	if string(output.Metrics["items"]) != `1` {
		t.Fatalf("metrics 应进入 manifest: %+v", output.Metrics)
	}
}

func TestPreviewFailsAndSkipsDownstreamWhenNodeFails(t *testing.T) {
	catalog := editorCatalog(t)
	repo := &memoryWorkflowRepo{draft: editorDraft()}
	sinkRunner, ok := editorDryRuns(t).Lookup(domain.CapabilityRef{Kind: "test.sink", Version: 1})
	if !ok {
		t.Fatal("缺少 sink stub")
	}
	failing, err := NewDryRunRegistry(
		stubDryRunner{ref: domain.CapabilityRef{Kind: "test.source", Version: 1},
			run: func(port.WorkflowDryRunExecution) (port.WorkflowDryRunResult, error) {
				return port.WorkflowDryRunResult{}, portDryRunError("样本解析失败")
			}},
		sinkRunner,
	)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewPreviewService(repo, catalog, failing)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.Create(context.Background(), repo.draft.ID, CreatePreviewRequest{
		Input: json.RawMessage(`{"inputs":{"input":{"resource_id":"raw-1"}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Status != domain.PreviewFailed {
		t.Fatalf("节点失败时预览应失败: %+v", preview)
	}
	found := false
	for _, issue := range preview.Issues {
		if issue.Code == "preview_node_failed" && issue.Path == "nodes.source" && issue.Severity == domain.SeverityError {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺少 preview_node_failed issue: %+v", preview.Issues)
	}
	manifest := decodeManifest(t, preview.OutputManifest)
	if manifest.Nodes["source"].Status != "failed" || manifest.Nodes["sink"].Status != "skipped" {
		t.Fatalf("失败后下游应 skipped: %+v", manifest.Nodes)
	}
}

func TestPreviewRejectsUnknownInputShape(t *testing.T) {
	catalog := editorCatalog(t)
	repo := &memoryWorkflowRepo{draft: editorDraft()}
	service, err := NewPreviewService(repo, catalog, editorDryRuns(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), repo.draft.ID, CreatePreviewRequest{
		Input: json.RawMessage(`{"input":{"resource_id":"raw-1"}}`)}); err == nil {
		t.Fatal("input 只允许 inputs/samples 两个顶级键")
	}
	if _, err := service.Create(context.Background(), repo.draft.ID, CreatePreviewRequest{
		Input: json.RawMessage(`{"inputs":{"input":{}}}`)}); err == nil {
		t.Fatal("input 引用必须携带 resource_id")
	}
}

func TestAcceptanceRerunsCaseInputAndComparesExpectation(t *testing.T) {
	catalog := editorCatalog(t)
	repo := &memoryWorkflowRepo{draft: editorDraft()}
	service, _ := NewDraftService(repo, catalog)
	previewService, err := NewPreviewService(repo, catalog, editorDryRuns(t))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ExecuteCommand(context.Background(), repo.draft.ID, CommandRequest{CommandID: "case-command",
		ExpectedRevision: 1, Type: "upsert_acceptance_case", Payload: json.RawMessage(
			`{"id":"case_one","name":"样本","input":{"inputs":{"input":{"resource_id":"raw-9"}}},` +
				`"expectation":{"nodes":{"sink":{"simulated":true,"outputs":{"out":{"metrics":{"items":1}}}}}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := previewService.RunAcceptance(context.Background(), repo.draft.ID, "case_one")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || len(result.Mismatches) != 0 {
		t.Fatalf("验收应通过: passed=%v mismatches=%+v", result.Passed, result.Mismatches)
	}
	passed := result.Draft.AcceptanceCases[0]
	if !passed.LastPassed || passed.LastPassedRevision != 2 || passed.LastPreviewID != result.Preview.ID {
		t.Fatalf("通过结果未原子写入: %+v", passed)
	}
}

func TestAcceptanceMismatchBlocksPass(t *testing.T) {
	catalog := editorCatalog(t)
	repo := &memoryWorkflowRepo{draft: editorDraft()}
	service, _ := NewDraftService(repo, catalog)
	previewService, err := NewPreviewService(repo, catalog, editorDryRuns(t))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ExecuteCommand(context.Background(), repo.draft.ID, CommandRequest{CommandID: "case-command",
		ExpectedRevision: 1, Type: "upsert_acceptance_case", Payload: json.RawMessage(
			`{"id":"case_one","name":"样本","input":{"inputs":{"input":{"resource_id":"raw-9"}}},` +
				`"expectation":{"nodes":{"sink":{"simulated":false,"outputs":{"out":{"metrics":{"items":2}}}}}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := previewService.RunAcceptance(context.Background(), repo.draft.ID, "case_one")
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed {
		t.Fatal("expectation 不匹配时不应通过")
	}
	if len(result.Mismatches) != 2 {
		t.Fatalf("应报告 simulated 与 items 两处差异: %+v", result.Mismatches)
	}
	if result.Draft.AcceptanceCases[0].LastPassed {
		t.Fatal("失败时不应盖章 LastPassed")
	}
}
