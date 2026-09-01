package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reqflow/internal/domain/logic"
	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

// WorkflowKnowledgeAnalyzeDryRunner 处理 knowledge.analyze 的预览。分析
// 依赖 LLM Agent 循环，预览不允许伪造成功：输出必须来自 input.samples 的
// 显式样本，并按冻结 OutputContract Schema 校验后标记模拟。
type WorkflowKnowledgeAnalyzeDryRunner struct{}

func NewWorkflowKnowledgeAnalyzeDryRunner() (*WorkflowKnowledgeAnalyzeDryRunner, error) {
	return &WorkflowKnowledgeAnalyzeDryRunner{}, nil
}

func (*WorkflowKnowledgeAnalyzeDryRunner) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "knowledge.analyze", Version: 1}
}

func (WorkflowKnowledgeAnalyzeDryRunner) DryRun(_ context.Context, execution port.WorkflowDryRunExecution) (port.WorkflowDryRunResult, error) {
	return explicitAnalysisSample(execution, "knowledge.analyze", "knowledge", "analysis")
}

// WorkflowApproveAnalysisDryRunner 处理 human.approve_analysis 的预览：
// 人工确认样本同样必须显式提供并通过 OutputContract 校验。
type WorkflowApproveAnalysisDryRunner struct{}

func NewWorkflowApproveAnalysisDryRunner() (*WorkflowApproveAnalysisDryRunner, error) {
	return &WorkflowApproveAnalysisDryRunner{}, nil
}

func (*WorkflowApproveAnalysisDryRunner) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "human.approve_analysis", Version: 1}
}

func (WorkflowApproveAnalysisDryRunner) DryRun(_ context.Context, execution port.WorkflowDryRunExecution) (port.WorkflowDryRunResult, error) {
	return explicitAnalysisSample(execution, "human.approve_analysis", "", "approved")
}

func explicitAnalysisSample(execution port.WorkflowDryRunExecution, capability, requiredUpstreamPort, outputPort string) (port.WorkflowDryRunResult, error) {
	if requiredUpstreamPort != "" {
		// 上游既可以是节点样本，也可以是流程输入提供的正式快照引用（链头
		// 场景）；预览不读取正式快照内容，输出仍由显式样本表达。
		_, upstream := execution.Samples.Upstream(requiredUpstreamPort)
		_, formal := analysisWorkflowInput(execution.Inputs, requiredUpstreamPort)
		if !upstream && !formal {
			return port.WorkflowDryRunResult{}, fmt.Errorf("%s 预览缺少上游 %s 输入", capability, requiredUpstreamPort)
		}
	}
	if execution.Rules.OutputContract == nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("%s 预览需要内联 OutputContract", capability)
	}
	raw, ok := execution.Samples.Explicit(outputPort)
	if !ok {
		return port.WorkflowDryRunResult{}, fmt.Errorf("%s 预览需要 input.samples 提供显式样本 %s", capability, outputPort)
	}
	schema, schemaHash, err := domain.CompileOutputContract(*execution.Rules.OutputContract)
	if err != nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("编译 OutputContract: %w", err)
	}
	output, err := logic.NormalizeDatasetItem(schema, raw)
	if err != nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("%s 样本不符合 OutputContract: %w", capability, err)
	}
	return port.WorkflowDryRunResult{
		Simulated: true,
		Outputs: []domain.NodeResourceBinding{{Port: outputPort,
			ResourceType: domain.ResourceAnalysisResult,
			ResourceID:   domain.TemporaryResourceID(execution.PreviewID, execution.Node.ID, outputPort)}},
		Samples: map[string]json.RawMessage{outputPort: output},
		Metrics: map[string]any{"output_schema_hash": schemaHash},
	}, nil
}

// WorkflowArtifactRenderDryRunner 处理 artifact.render 的预览。渲染内容在
// 内存中真实生成，但不落 Artifact；输出标记模拟。
type WorkflowArtifactRenderDryRunner struct{}

func NewWorkflowArtifactRenderDryRunner() (*WorkflowArtifactRenderDryRunner, error) {
	return &WorkflowArtifactRenderDryRunner{}, nil
}

func (*WorkflowArtifactRenderDryRunner) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "artifact.render", Version: 1}
}

func (WorkflowArtifactRenderDryRunner) DryRun(_ context.Context, execution port.WorkflowDryRunExecution) (port.WorkflowDryRunResult, error) {
	upstream, ok := execution.Samples.Upstream("analysis")
	if !ok {
		return port.WorkflowDryRunResult{}, fmt.Errorf("artifact.render 预览缺少上游 analysis 样本")
	}
	if execution.Rules.OutputContract == nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("artifact.render 预览需要内联 OutputContract")
	}
	var config artifactRenderConfig
	if err := json.Unmarshal(execution.Node.Config, &config); err != nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("artifact.render config 非法: %w", err)
	}
	content, err := renderArtifactContent(upstream.Payload, config.Kind, config.ContentPath)
	if err != nil {
		return port.WorkflowDryRunResult{}, err
	}
	sample, err := json.Marshal(map[string]any{"kind": strings.TrimSpace(config.Kind),
		"name": strings.TrimSpace(config.Name), "bytes": len(content),
		"content": string(content), "simulated": true})
	if err != nil {
		return port.WorkflowDryRunResult{}, err
	}
	return port.WorkflowDryRunResult{
		Simulated: true,
		Outputs: []domain.NodeResourceBinding{{Port: "artifact",
			ResourceType: domain.ResourceArtifact,
			ResourceID:   domain.TemporaryResourceID(execution.PreviewID, execution.Node.ID, "artifact")}},
		Samples: map[string]json.RawMessage{"artifact": sample},
		Metrics: map[string]any{"kind": strings.TrimSpace(config.Kind), "bytes": len(content)},
	}, nil
}
