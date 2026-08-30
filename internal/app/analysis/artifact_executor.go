package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"reqflow/internal/app/orchestrator"
	"reqflow/internal/domain/model"
)

type ArtifactRenderExecutor struct {
	analysis  *Service
	artifacts *ArtifactService
}

func NewArtifactRenderExecutor(analysis *Service, artifacts *ArtifactService) (*ArtifactRenderExecutor, error) {
	if analysis == nil || artifacts == nil {
		return nil, fmt.Errorf("artifact.render 依赖不完整")
	}
	return &ArtifactRenderExecutor{analysis: analysis, artifacts: artifacts}, nil
}

func (*ArtifactRenderExecutor) Kind() model.StepKind { return model.StepKindArtifactRender }

type artifactRenderConfig struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	ContentPath string `json:"content_path,omitempty"`
}

func (*ArtifactRenderExecutor) ValidateDefinition(_ context.Context, step model.StepDefinition) error {
	if len(step.Inputs) != 1 || strings.TrimSpace(step.Inputs["analysis"]) == "" {
		return fmt.Errorf("artifact.render 必须且只能声明 analysis 输入")
	}
	if len(step.Outputs) != 1 || step.Outputs["artifact"] != model.ResourceArtifact {
		return fmt.Errorf("artifact.render 必须且只能输出 artifact")
	}
	_, err := decodeArtifactRenderConfig(step.Config)
	return err
}

func (e *ArtifactRenderExecutor) Execute(ctx context.Context, run orchestrator.StepRunContext) (orchestrator.StepResult, error) {
	config, err := decodeArtifactRenderConfig(run.Config)
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	ref := run.Inputs["analysis"]
	if ref.ResourceType != model.ResourceAnalysisResult {
		return orchestrator.StepResult{}, fmt.Errorf("artifact.render analysis 输入必须是 analysis_result")
	}
	result, err := e.analysis.GetResult(ctx, ref.ResourceID)
	if err != nil || result.Status != model.AnalysisResultSucceeded {
		return orchestrator.StepResult{}, fmt.Errorf("AnalysisResult 不存在或未成功")
	}
	var output any
	if err := json.Unmarshal(result.Output, &output); err != nil {
		return orchestrator.StepResult{}, err
	}
	selected, err := jsonPath(output, config.ContentPath)
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	var content []byte
	if config.Kind == model.ArtifactMarkdown {
		text, ok := selected.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return orchestrator.StepResult{}, fmt.Errorf("markdown content_path 必须指向非空字符串")
		}
		content = []byte(text)
	} else {
		content, err = json.MarshalIndent(selected, "", "  ")
		if err != nil {
			return orchestrator.StepResult{}, err
		}
	}
	artifact, err := e.artifacts.Publish(ctx, PublishArtifactInput{WorkspaceID: result.WorkspaceID,
		Kind: config.Kind, Name: config.Name, Content: content, SourceTaskID: run.TaskID,
		SourceStepRunID: run.StepRunID, ProducerAttempt: run.Attempt,
		Metadata: map[string]any{"analysis_result_id": result.ID, "analysis_profile_id": result.AnalysisProfileID}})
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	boundary, _ := json.Marshal(model.ArtifactBoundary{ArtifactID: artifact.ID,
		Kind: artifact.Kind, ContentHash: artifact.ContentHash})
	return orchestrator.StepResult{Outputs: map[string]model.ResourceRef{"artifact": {
		ResourceType: model.ResourceArtifact, ResourceID: artifact.ID, Boundary: boundary,
	}}, Metrics: map[string]any{"kind": artifact.Kind, "content_hash": artifact.ContentHash}}, nil
}

func (e *ArtifactRenderExecutor) Resume(ctx context.Context, run orchestrator.StepRunContext,
	_ json.RawMessage) (orchestrator.StepResult, error) {
	return e.Execute(ctx, run)
}

func decodeArtifactRenderConfig(raw json.RawMessage) (artifactRenderConfig, error) {
	var config artifactRenderConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("artifact.render config 非法: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return config, fmt.Errorf("artifact.render config 只能包含一个 JSON object")
	}
	config.Name, config.Kind, config.ContentPath = strings.TrimSpace(config.Name),
		strings.TrimSpace(config.Kind), strings.TrimSpace(config.ContentPath)
	if config.Name == "" {
		return config, fmt.Errorf("artifact.render name 不能为空")
	}
	if config.Kind != model.ArtifactMarkdown && config.Kind != model.ArtifactJSON {
		return config, fmt.Errorf("artifact.render kind 必须是 markdown 或 json")
	}
	if config.Kind == model.ArtifactMarkdown && config.ContentPath == "" {
		return config, fmt.Errorf("markdown artifact 必须配置 content_path")
	}
	return config, nil
}
