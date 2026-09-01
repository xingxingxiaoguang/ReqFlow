package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reqflow/internal/domain/model"
	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type WorkflowKnowledgeAnalyzeExecutor struct {
	service *Service
}

func NewWorkflowKnowledgeAnalyzeExecutor(service *Service) (*WorkflowKnowledgeAnalyzeExecutor, error) {
	if service == nil {
		return nil, fmt.Errorf("workflow knowledge.analyze: analysis service is required")
	}
	return &WorkflowKnowledgeAnalyzeExecutor{service: service}, nil
}

func (*WorkflowKnowledgeAnalyzeExecutor) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "knowledge.analyze", Version: 1}
}

func (e *WorkflowKnowledgeAnalyzeExecutor) Execute(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	return e.run(ctx, execution)
}

func (e *WorkflowKnowledgeAnalyzeExecutor) Resume(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	return e.run(ctx, execution)
}

type knowledgeAnalyzeConfig struct {
	Instruction          string `json:"instruction"`
	KnowledgeName        string `json:"knowledge_name"`
	KnowledgeDescription string `json:"knowledge_description"`
}

func (e *WorkflowKnowledgeAnalyzeExecutor) run(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	input, ok := analysisWorkflowInput(execution.Inputs, "knowledge")
	if !ok || input.ResourceType != domain.ResourceRetrievalSnapshot || strings.TrimSpace(input.ResourceID) == "" {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("knowledge.analyze knowledge 输入必须是具体 RetrievalSnapshot")
	}
	if execution.Rules.OutputContract == nil {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("knowledge.analyze 缺少内联 OutputContract")
	}
	var config knowledgeAnalyzeConfig
	if err := json.Unmarshal(execution.Node.Config, &config); err != nil {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("knowledge.analyze config 非法: %w", err)
	}
	config.Instruction = strings.TrimSpace(config.Instruction)
	config.KnowledgeName = strings.TrimSpace(config.KnowledgeName)
	if config.Instruction == "" {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("knowledge.analyze instruction 不能为空")
	}
	if config.KnowledgeName == "" {
		config.KnowledgeName = "knowledge"
	}
	result, err := e.service.Analyze(ctx, RunInput{WorkspaceID: execution.WorkspaceID,
		WorkflowRunID: execution.RunID, NodeRunID: execution.NodeRunID,
		ProducerAttempt: execution.Attempt, Instruction: config.Instruction,
		OutputContract: *execution.Rules.OutputContract,
		Knowledge: map[string]KnowledgeSourceInput{"knowledge": {
			Name: config.KnowledgeName, Description: strings.TrimSpace(config.KnowledgeDescription),
		}},
		Inputs:     map[string]domain.NodeResourceBinding{"knowledge": input},
		Checkpoint: execution.Checkpoint,
		SaveCheckpoint: func(raw json.RawMessage) error {
			return execution.CheckpointWriter.Save(ctx, raw)
		},
		ReportProgress: func(progress map[string]any) error {
			raw, marshalErr := json.Marshal(progress)
			if marshalErr != nil {
				return marshalErr
			}
			return execution.Progress.Report(ctx, raw)
		},
	})
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	boundary, err := json.Marshal(model.AnalysisResultBoundary{AnalysisResultID: result.ID,
		OutputContractHash: result.OutputContractHash, Model: result.Model})
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	return port.WorkflowCapabilityResult{Outputs: []domain.NodeResourceBinding{{Port: "analysis",
		ResourceType: domain.ResourceAnalysisResult, ResourceID: result.ID, Boundary: boundary}},
		Metrics: map[string]any{"status": result.Status, "model": result.Model,
			"input_tokens": result.InputTokens, "output_tokens": result.OutputTokens}}, nil
}

type WorkflowArtifactRenderExecutor struct {
	analysis  *Service
	artifacts *ArtifactService
}

func NewWorkflowArtifactRenderExecutor(analysis *Service, artifacts *ArtifactService) (*WorkflowArtifactRenderExecutor, error) {
	if analysis == nil || artifacts == nil {
		return nil, fmt.Errorf("workflow artifact.render: analysis and artifact services are required")
	}
	return &WorkflowArtifactRenderExecutor{analysis: analysis, artifacts: artifacts}, nil
}

func (*WorkflowArtifactRenderExecutor) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "artifact.render", Version: 1}
}

func (e *WorkflowArtifactRenderExecutor) Execute(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	return e.run(ctx, execution)
}

func (e *WorkflowArtifactRenderExecutor) Resume(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	return e.run(ctx, execution)
}

type artifactRenderConfig struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	ContentPath string `json:"content_path"`
}

func (e *WorkflowArtifactRenderExecutor) run(ctx context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	input, ok := analysisWorkflowInput(execution.Inputs, "analysis")
	if !ok || input.ResourceType != domain.ResourceAnalysisResult || strings.TrimSpace(input.ResourceID) == "" {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("artifact.render analysis 输入必须是具体 AnalysisResult")
	}
	if execution.Rules.OutputContract == nil {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("artifact.render 缺少内联 OutputContract")
	}
	var config artifactRenderConfig
	if err := json.Unmarshal(execution.Node.Config, &config); err != nil {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("artifact.render config 非法: %w", err)
	}
	result, err := e.analysis.GetResult(ctx, input.ResourceID)
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	if result.Status != model.AnalysisResultSucceeded {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("artifact.render 只能消费 succeeded AnalysisResult")
	}
	if result.WorkspaceID != execution.WorkspaceID {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("AnalysisResult 不属于当前 workspace")
	}
	contractHash, err := domain.HashContract(*execution.Rules.OutputContract)
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	if result.OutputContractHash != contractHash {
		return port.WorkflowCapabilityResult{}, fmt.Errorf("AnalysisResult OutputContract 与当前 Revision 不一致")
	}
	content, err := renderArtifactContent(result.Output, config.Kind, config.ContentPath)
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	artifact, err := e.artifacts.Publish(ctx, PublishArtifactInput{WorkspaceID: execution.WorkspaceID,
		Kind: strings.TrimSpace(config.Kind), Name: strings.TrimSpace(config.Name), Content: content,
		ProducerWorkflowRunID: execution.RunID, ProducerNodeRunID: execution.NodeRunID,
		ProducerAttempt: execution.Attempt, Metadata: map[string]any{
			"analysis_result_id": result.ID, "output_contract_hash": result.OutputContractHash,
			"content_path": strings.TrimSpace(config.ContentPath),
		}})
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	boundary, err := json.Marshal(model.ArtifactBoundary{ArtifactID: artifact.ID,
		Kind: artifact.Kind, ContentHash: artifact.ContentHash})
	if err != nil {
		return port.WorkflowCapabilityResult{}, err
	}
	return port.WorkflowCapabilityResult{Outputs: []domain.NodeResourceBinding{{Port: "artifact",
		ResourceType: domain.ResourceArtifact, ResourceID: artifact.ID, Boundary: boundary}},
		Metrics: map[string]any{"kind": artifact.Kind, "content_hash": artifact.ContentHash,
			"bytes": len(content)}}, nil
}

func renderArtifactContent(output json.RawMessage, kind, path string) ([]byte, error) {
	var root any
	if err := json.Unmarshal(output, &root); err != nil {
		return nil, fmt.Errorf("AnalysisResult output 非法: %w", err)
	}
	selected, err := jsonPath(root, path)
	if err != nil {
		return nil, err
	}
	switch strings.TrimSpace(kind) {
	case model.ArtifactJSON, model.ArtifactGraphManifest:
		content, marshalErr := json.MarshalIndent(selected, "", "  ")
		if marshalErr != nil {
			return nil, marshalErr
		}
		return append(content, '\n'), nil
	case model.ArtifactMarkdown:
		if text, ok := selected.(string); ok {
			return []byte(text), nil
		}
		content, marshalErr := json.MarshalIndent(selected, "", "  ")
		if marshalErr != nil {
			return nil, marshalErr
		}
		return []byte("```json\n" + string(content) + "\n```\n"), nil
	default:
		return nil, fmt.Errorf("artifact.render kind %q 不受支持", kind)
	}
}

func analysisWorkflowInput(inputs []domain.NodeResourceBinding, portName string) (domain.NodeResourceBinding, bool) {
	for _, input := range inputs {
		if input.Direction == domain.BindingInput && input.Port == portName {
			return input, true
		}
	}
	return domain.NodeResourceBinding{}, false
}
