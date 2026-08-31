package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"

	"reqflow/internal/app/orchestrator"
	"reqflow/internal/domain/model"
)

type KnowledgeAnalyzeExecutor struct{ service *Service }

func NewKnowledgeAnalyzeExecutor(service *Service) (*KnowledgeAnalyzeExecutor, error) {
	if service == nil {
		return nil, fmt.Errorf("knowledge.analyze: service is required")
	}
	return &KnowledgeAnalyzeExecutor{service: service}, nil
}

func (*KnowledgeAnalyzeExecutor) Kind() model.StepKind { return model.StepKindKnowledgeAnalyze }

type KnowledgeAnalyzeConfig struct {
	AnalysisProfileID string                          `json:"analysis_profile_id"`
	KnowledgeSources  map[string]KnowledgeSourceInput `json:"knowledge_sources"`
}

func (e *KnowledgeAnalyzeExecutor) ValidateDefinition(ctx context.Context, step model.StepDefinition) error {
	config, err := decodeKnowledgeAnalyzeConfig(step.Config)
	if err != nil {
		return err
	}
	if len(step.Inputs) == 0 || len(step.Inputs) != len(config.KnowledgeSources) {
		return fmt.Errorf("knowledge.analyze 的每个输入都必须声明为 knowledge_sources")
	}
	for portName := range step.Inputs {
		if _, ok := config.KnowledgeSources[portName]; !ok {
			return fmt.Errorf("knowledge_sources 缺少输入端口 %s", portName)
		}
	}
	if len(step.Outputs) != 1 || step.Outputs["analysis"] != model.ResourceAnalysisResult {
		return fmt.Errorf("knowledge.analyze 必须且只能输出 analysis: analysis_result")
	}
	if _, err := e.service.GetProfile(ctx, config.AnalysisProfileID); err != nil {
		return fmt.Errorf("AnalysisProfile 不存在: %w", err)
	}
	return nil
}

func (e *KnowledgeAnalyzeExecutor) Execute(ctx context.Context, run orchestrator.StepRunContext) (orchestrator.StepResult, error) {
	return e.run(ctx, run, nil)
}

func (e *KnowledgeAnalyzeExecutor) Resume(ctx context.Context, run orchestrator.StepRunContext,
	checkpoint json.RawMessage) (orchestrator.StepResult, error) {
	return e.run(ctx, run, checkpoint)
}

func (e *KnowledgeAnalyzeExecutor) run(ctx context.Context, run orchestrator.StepRunContext,
	checkpoint json.RawMessage) (orchestrator.StepResult, error) {
	config, err := decodeKnowledgeAnalyzeConfig(run.Config)
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	result, err := e.service.Analyze(ctx, RunInput{TaskID: run.TaskID,
		StepRunID: run.StepRunID, ProducerAttempt: run.Attempt,
		AnalysisProfileID: config.AnalysisProfileID, Knowledge: config.KnowledgeSources,
		Inputs: run.Inputs, Checkpoint: checkpoint,
		SaveCheckpoint: func(raw json.RawMessage) error { return run.Checkpoint.Save(ctx, raw) },
		ReportProgress: func(progress map[string]any) error {
			raw, marshalErr := json.Marshal(progress)
			if marshalErr != nil {
				return marshalErr
			}
			return run.Progress.Report(ctx, raw)
		}})
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	profile, err := e.service.GetProfile(ctx, result.AnalysisProfileID)
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	boundary, _ := json.Marshal(model.AnalysisResultBoundary{AnalysisResultID: result.ID,
		ProfileID: profile.ID, ProfileHash: profile.ProfileHash, Model: result.Model})
	return orchestrator.StepResult{Outputs: map[string]model.ResourceRef{"analysis": {
		ResourceType: model.ResourceAnalysisResult, ResourceID: result.ID, Boundary: boundary,
	}}, Metrics: map[string]any{"status": result.Status, "input_tokens": result.InputTokens,
		"output_tokens": result.OutputTokens, "cache_read_tokens": result.CacheReadTokens,
		"cache_write_tokens": result.CacheWriteTokens}}, nil
}

func decodeKnowledgeAnalyzeConfig(raw json.RawMessage) (KnowledgeAnalyzeConfig, error) {
	var config KnowledgeAnalyzeConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(bytes.TrimSpace(raw)) == 0 {
		return config, fmt.Errorf("knowledge.analyze config 不能为空")
	}
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("knowledge.analyze config 非法: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return config, fmt.Errorf("knowledge.analyze config 只能包含一个 JSON object")
	}
	config.AnalysisProfileID = strings.TrimSpace(config.AnalysisProfileID)
	if _, err := uuid.Parse(config.AnalysisProfileID); err != nil {
		return config, fmt.Errorf("analysis_profile_id 必须是 UUID")
	}
	if len(config.KnowledgeSources) == 0 || len(config.KnowledgeSources) > 32 {
		return config, fmt.Errorf("knowledge_sources 必须包含 1..32 项")
	}
	seen := map[string]bool{}
	for portName, source := range config.KnowledgeSources {
		if strings.TrimSpace(portName) != portName {
			return config, fmt.Errorf("knowledge_sources 端口名不能包含首尾空白")
		}
		source.Name = strings.TrimSpace(source.Name)
		if portName == "" || source.Name == "" || seen[source.Name] {
			return config, fmt.Errorf("knowledge_sources 端口和逻辑名不能为空且逻辑名不能重复")
		}
		seen[source.Name] = true
		config.KnowledgeSources[portName] = source
	}
	return config, nil
}
