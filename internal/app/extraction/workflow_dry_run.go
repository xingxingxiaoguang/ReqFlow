package extraction

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reqflow/internal/domain/logic"
	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

// WorkflowDocumentExtractDryRunner 处理 document.extract 的预览。真实抽取
// 依赖 LLM Agent 循环；预览阶段不允许伪造成功，必须由 input.samples 提供
// 显式抽取样本，并逐条按冻结 DataContract Schema 校验。
type WorkflowDocumentExtractDryRunner struct{}

func NewWorkflowDocumentExtractDryRunner() (*WorkflowDocumentExtractDryRunner, error) {
	return &WorkflowDocumentExtractDryRunner{}, nil
}

func (*WorkflowDocumentExtractDryRunner) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "document.extract", Version: 1}
}

func (WorkflowDocumentExtractDryRunner) DryRun(_ context.Context, execution port.WorkflowDryRunExecution) (port.WorkflowDryRunResult, error) {
	if _, ok := execution.Samples.Upstream("documents"); !ok {
		if input, bound := workflowDryRunInput(execution.Inputs, "documents"); !bound ||
			strings.TrimSpace(input.ResourceID) == "" {
			return port.WorkflowDryRunResult{}, fmt.Errorf("document.extract 预览缺少 documents 输入")
		}
	}
	if execution.Rules.DataContract == nil || execution.Rules.Extraction == nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("document.extract 预览需要内联 DataContract 与 ExtractionSpec")
	}
	raw, ok := execution.Samples.Explicit("drafts")
	if !ok {
		return port.WorkflowDryRunResult{}, fmt.Errorf("document.extract 预览需要 input.samples 提供显式抽取样本 drafts")
	}
	schema, _, err := domain.CompileDataContract(*execution.Rules.DataContract)
	if err != nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("编译 DataContract: %w", err)
	}
	var sample struct {
		Records []struct {
			Fields json.RawMessage `json:"fields"`
		} `json:"records"`
	}
	if err := json.Unmarshal(raw, &sample); err != nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("抽取样本必须是 {\"records\":[{\"fields\":{...}}]}: %w", err)
	}
	if len(sample.Records) == 0 {
		return port.WorkflowDryRunResult{}, fmt.Errorf("抽取样本至少包含一条记录")
	}
	fields := make([]json.RawMessage, 0, len(sample.Records))
	for i, record := range sample.Records {
		normalized, normalizeErr := logic.NormalizeDatasetItem(schema, record.Fields)
		if normalizeErr != nil {
			return port.WorkflowDryRunResult{}, fmt.Errorf("第 %d 条抽取样本不符合 DataContract: %w", i+1, normalizeErr)
		}
		fields = append(fields, normalized)
	}
	payload, err := json.Marshal(map[string]any{"records": fields})
	if err != nil {
		return port.WorkflowDryRunResult{}, err
	}
	return port.WorkflowDryRunResult{
		Simulated: true,
		Outputs: []domain.NodeResourceBinding{{Port: "drafts",
			ResourceType: domain.ResourceRecordDrafts,
			ResourceID:   domain.TemporaryResourceID(execution.PreviewID, execution.Node.ID, "drafts")}},
		Samples: map[string]json.RawMessage{"drafts": payload},
		Metrics: map[string]any{"records": len(fields)},
	}, nil
}

func workflowDryRunInput(inputs []domain.NodeResourceBinding, portName string) (domain.NodeResourceBinding, bool) {
	for _, input := range inputs {
		if input.Port == portName && input.Direction == domain.BindingInput {
			return input, true
		}
	}
	return domain.NodeResourceBinding{}, false
}
