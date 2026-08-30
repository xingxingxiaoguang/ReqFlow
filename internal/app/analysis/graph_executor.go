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

// GraphBuildExecutor 不复制实体/关系抽取逻辑；它只把两个已提交 DatasetBatch 和
// 产生它们的 AnalysisResult 固化为可追溯 Graph Manifest。
type GraphBuildExecutor struct {
	analysis  *Service
	artifacts *ArtifactService
}

func NewGraphBuildExecutor(analysis *Service, artifacts *ArtifactService) (*GraphBuildExecutor, error) {
	if analysis == nil || artifacts == nil {
		return nil, fmt.Errorf("graph.build 依赖不完整")
	}
	return &GraphBuildExecutor{analysis: analysis, artifacts: artifacts}, nil
}

func (*GraphBuildExecutor) Kind() model.StepKind { return model.StepKindGraphBuild }

type graphBuildConfig struct {
	Name string `json:"name"`
}

func (*GraphBuildExecutor) ValidateDefinition(_ context.Context, step model.StepDefinition) error {
	if len(step.Inputs) != 3 || strings.TrimSpace(step.Inputs["analysis"]) == "" ||
		strings.TrimSpace(step.Inputs["nodes_batch"]) == "" || strings.TrimSpace(step.Inputs["edges_batch"]) == "" {
		return fmt.Errorf("graph.build 必须且只能声明 analysis、nodes_batch、edges_batch 输入")
	}
	if len(step.Outputs) != 1 || step.Outputs["manifest"] != model.ResourceArtifact {
		return fmt.Errorf("graph.build 必须且只能输出 manifest: artifact")
	}
	_, err := decodeGraphBuildConfig(step.Config)
	return err
}

func (e *GraphBuildExecutor) Execute(ctx context.Context, run orchestrator.StepRunContext) (orchestrator.StepResult, error) {
	config, err := decodeGraphBuildConfig(run.Config)
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	analysisRef, nodesRef, edgesRef := run.Inputs["analysis"], run.Inputs["nodes_batch"], run.Inputs["edges_batch"]
	if analysisRef.ResourceType != model.ResourceAnalysisResult || nodesRef.ResourceType != model.ResourceDatasetBatch ||
		edgesRef.ResourceType != model.ResourceDatasetBatch {
		return orchestrator.StepResult{}, fmt.Errorf("graph.build 输入类型非法")
	}
	result, err := e.analysis.GetResult(ctx, analysisRef.ResourceID)
	if err != nil || result.Status != model.AnalysisResultSucceeded {
		return orchestrator.StepResult{}, fmt.Errorf("AnalysisResult 不存在或未成功")
	}
	manifest := map[string]any{"version": 1, "analysis_result_id": result.ID,
		"nodes_batch": map[string]any{"id": nodesRef.ResourceID, "boundary": json.RawMessage(nodesRef.Boundary)},
		"edges_batch": map[string]any{"id": edgesRef.ResourceID, "boundary": json.RawMessage(edgesRef.Boundary)}}
	content, _ := json.MarshalIndent(manifest, "", "  ")
	artifact, err := e.artifacts.Publish(ctx, PublishArtifactInput{WorkspaceID: result.WorkspaceID,
		Kind: model.ArtifactGraphManifest, Name: config.Name, Content: content,
		SourceTaskID: run.TaskID, SourceStepRunID: run.StepRunID, ProducerAttempt: run.Attempt,
		Metadata: map[string]any{"analysis_result_id": result.ID,
			"nodes_batch_id": nodesRef.ResourceID, "edges_batch_id": edgesRef.ResourceID}})
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	boundary, _ := json.Marshal(model.ArtifactBoundary{ArtifactID: artifact.ID,
		Kind: artifact.Kind, ContentHash: artifact.ContentHash})
	return orchestrator.StepResult{Outputs: map[string]model.ResourceRef{"manifest": {
		ResourceType: model.ResourceArtifact, ResourceID: artifact.ID, Boundary: boundary,
	}}, Metrics: map[string]any{"nodes_batch_id": nodesRef.ResourceID,
		"edges_batch_id": edgesRef.ResourceID, "content_hash": artifact.ContentHash}}, nil
}

func (e *GraphBuildExecutor) Resume(ctx context.Context, run orchestrator.StepRunContext,
	_ json.RawMessage) (orchestrator.StepResult, error) {
	return e.Execute(ctx, run)
}

func decodeGraphBuildConfig(raw json.RawMessage) (graphBuildConfig, error) {
	var config graphBuildConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("graph.build config 非法: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return config, fmt.Errorf("graph.build config 只能包含一个 JSON object")
	}
	config.Name = strings.TrimSpace(config.Name)
	if config.Name == "" {
		return config, fmt.Errorf("graph.build name 不能为空")
	}
	return config, nil
}
