package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

// WorkflowApproveAnalysisManualCompleter 处理 human.approve_analysis 的人工
// 完成：不复用客户端提交的 AnalysisResult ID，而是为人工 NodeRun 生产一条
// 合同一致、Schema 校验通过的 succeeded AnalysisResult，供 artifact.render
// 正常消费。
type WorkflowApproveAnalysisManualCompleter struct {
	repo port.AnalysisRepo
}

func NewWorkflowApproveAnalysisManualCompleter(repo port.AnalysisRepo) (*WorkflowApproveAnalysisManualCompleter, error) {
	if repo == nil {
		return nil, fmt.Errorf("workflow human.approve_analysis: analysis repository is required")
	}
	return &WorkflowApproveAnalysisManualCompleter{repo: repo}, nil
}

func (*WorkflowApproveAnalysisManualCompleter) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "human.approve_analysis", Version: 1}
}

type approveAnalysisPayload struct {
	Decision  string          `json:"decision"`
	Output    json.RawMessage `json:"output,omitempty"`
	Rationale string          `json:"rationale"`
}

func (e *WorkflowApproveAnalysisManualCompleter) Complete(ctx context.Context, execution port.WorkflowManualExecution) ([]domain.NodeResourceBinding, error) {
	input, ok := analysisWorkflowInput(execution.Inputs, "analysis")
	if !ok || input.ResourceType != domain.ResourceAnalysisResult || strings.TrimSpace(input.ResourceID) == "" {
		return nil, fmt.Errorf("human.approve_analysis 输入必须是具体 AnalysisResult")
	}
	if execution.Rules.OutputContract == nil {
		return nil, fmt.Errorf("当前 Revision 缺少内联 OutputContract")
	}
	var payload approveAnalysisPayload
	if err := json.Unmarshal(execution.Payload, &payload); err != nil {
		return nil, fmt.Errorf("人工确认分析载荷非法: %w", err)
	}
	payload.Rationale = strings.TrimSpace(payload.Rationale)
	if payload.Rationale == "" {
		return nil, fmt.Errorf("人工确认分析必须提供 rationale")
	}
	source, err := e.repo.GetAnalysisResult(ctx, input.ResourceID)
	if err != nil {
		return nil, fmt.Errorf("读取 AnalysisResult: %w", err)
	}
	if source.Status != model.AnalysisResultSucceeded {
		return nil, fmt.Errorf("AnalysisResult %s 状态 %s 不允许人工确认", source.ID, source.Status)
	}
	if source.WorkspaceID != execution.WorkspaceID {
		return nil, fmt.Errorf("AnalysisResult 不属于当前 workspace")
	}
	outputSchema, outputSchemaHash, err := domain.CompileOutputContract(*execution.Rules.OutputContract)
	if err != nil {
		return nil, fmt.Errorf("编译 OutputContract: %w", err)
	}
	outputContractRaw, err := json.Marshal(*execution.Rules.OutputContract)
	if err != nil {
		return nil, err
	}
	outputContractHash, err := domain.HashContract(*execution.Rules.OutputContract)
	if err != nil {
		return nil, err
	}
	if source.OutputContractHash != outputContractHash {
		return nil, fmt.Errorf("AnalysisResult OutputContract 与当前 Revision 不一致")
	}
	output := source.Output
	if payload.Decision == "edit" {
		if len(payload.Output) == 0 {
			return nil, fmt.Errorf("编辑确认必须提供 output")
		}
		output = payload.Output
	} else if payload.Decision != "approve" {
		return nil, fmt.Errorf("人工确认动作 %q 非法", payload.Decision)
	}
	normalized, err := logic.NormalizeDatasetItem(outputSchema, output)
	if err != nil {
		return nil, fmt.Errorf("人工确认输出不符合 OutputContract: %w", err)
	}
	agentContext, err := json.Marshal(map[string]any{"producer": "human", "actor": execution.Actor,
		"decision": payload.Decision, "rationale": payload.Rationale, "source_analysis_result_id": source.ID})
	if err != nil {
		return nil, err
	}
	result, err := e.repo.CreateHumanApprovedAnalysis(ctx, &model.AnalysisResult{
		WorkspaceID: execution.WorkspaceID, Instruction: source.Instruction,
		OutputContract: outputContractRaw, OutputContractHash: outputContractHash,
		OutputSchema: outputSchema, OutputSchemaHash: outputSchemaHash,
		ProducerWorkflowRunID: execution.RunID, ProducerNodeRunID: execution.NodeRunID,
		ProducerAttempt: execution.Attempt, Status: model.AnalysisResultSucceeded,
		Output: normalized, AgentContext: agentContext, Model: "human",
	})
	if err != nil {
		return nil, err
	}
	boundary, err := json.Marshal(model.AnalysisResultBoundary{AnalysisResultID: result.ID,
		OutputContractHash: result.OutputContractHash, Model: result.Model})
	if err != nil {
		return nil, err
	}
	return []domain.NodeResourceBinding{{Port: "approved",
		ResourceType: domain.ResourceAnalysisResult, ResourceID: result.ID, Boundary: boundary}}, nil
}
