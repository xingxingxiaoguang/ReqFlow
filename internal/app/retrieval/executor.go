package retrieval

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

type BuildExecutor struct {
	service *Service
}

func NewBuildExecutor(service *Service) (*BuildExecutor, error) {
	if service == nil {
		return nil, fmt.Errorf("retrieval.build: service is required")
	}
	return &BuildExecutor{service: service}, nil
}

func (*BuildExecutor) Kind() model.StepKind { return model.StepKindRetrievalBuild }

type buildConfig struct {
	RetrievalProfileID string `json:"retrieval_profile_id"`
}

func (e *BuildExecutor) ValidateDefinition(ctx context.Context, step model.StepDefinition) error {
	if err := validateBuildPorts(step); err != nil {
		return err
	}
	config, err := decodeBuildConfig(step.Config)
	if err != nil {
		return err
	}
	// 定义级允许 retrieval_profile_id 留空占位，由任务创建时按数据集 schema 落实；
	// 旧定义写死了规则时仍然就地校验。
	return e.validateProfile(ctx, config)
}

// ValidateRuntimeStep 实现任务创建级校验：索引规则此时必须已落实。
func (e *BuildExecutor) ValidateRuntimeStep(ctx context.Context, step model.StepDefinition) error {
	if err := validateBuildPorts(step); err != nil {
		return err
	}
	config, err := decodeBuildConfig(step.Config)
	if err != nil {
		return err
	}
	if config.RetrievalProfileID == "" {
		return fmt.Errorf("retrieval.build 的 retrieval_profile_id 必须在创建任务时按目标数据集字段选择")
	}
	return e.validateProfile(ctx, config)
}

func (e *BuildExecutor) validateProfile(ctx context.Context, config buildConfig) error {
	if config.RetrievalProfileID == "" {
		return nil
	}
	if _, err := uuid.Parse(config.RetrievalProfileID); err != nil {
		return fmt.Errorf("retrieval_profile_id 必须是 UUID")
	}
	if _, err := e.service.GetProfile(ctx, config.RetrievalProfileID); err != nil {
		return fmt.Errorf("RetrievalProfile 不存在: %w", err)
	}
	return nil
}

func validateBuildPorts(step model.StepDefinition) error {
	if len(step.Inputs) != 1 || strings.TrimSpace(step.Inputs["dataset"]) == "" {
		return fmt.Errorf("retrieval.build 必须且只能声明 dataset 输入")
	}
	if len(step.Outputs) != 1 || step.Outputs["snapshot"] != model.ResourceRetrievalSnapshot {
		return fmt.Errorf("retrieval.build 必须且只能声明 snapshot: retrieval_snapshot 输出")
	}
	return nil
}

func (e *BuildExecutor) Execute(ctx context.Context, run orchestrator.StepRunContext) (orchestrator.StepResult, error) {
	input, ok := run.Inputs["dataset"]
	if !ok || input.ResourceType != model.ResourceDatasetBoundary || strings.TrimSpace(input.ResourceID) == "" {
		return orchestrator.StepResult{}, fmt.Errorf("retrieval.build dataset 输入必须是固定 DatasetBoundary")
	}
	var boundary model.DatasetBoundary
	if err := json.Unmarshal(input.Boundary, &boundary); err != nil {
		return orchestrator.StepResult{}, fmt.Errorf("dataset boundary 非法: %w", err)
	}
	if boundary.DatasetID != input.ResourceID || boundary.ThroughSeq < 0 {
		return orchestrator.StepResult{}, fmt.Errorf("dataset boundary 与资源不一致")
	}
	config, err := decodeBuildConfig(run.Config)
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	if config.RetrievalProfileID == "" {
		return orchestrator.StepResult{}, fmt.Errorf("retrieval.build 的 retrieval_profile_id 未在任务创建时落实")
	}
	snapshot, err := e.service.BuildSnapshot(ctx, BuildInput{DatasetID: input.ResourceID,
		RetrievalProfileID: config.RetrievalProfileID, SourceSeq: boundary.ThroughSeq,
		TaskID: run.TaskID, StepRunID: run.StepRunID, ProducerAttempt: run.Attempt,
	}, func(progress BuildProgress) error {
		checkpoint, marshalErr := json.Marshal(map[string]any{"snapshot_id": progress.SnapshotID,
			"processed_seq": progress.ProcessedSeq, "source_seq": progress.SourceSeq})
		if marshalErr != nil {
			return marshalErr
		}
		if err := run.Checkpoint.Save(ctx, checkpoint); err != nil {
			return err
		}
		event, marshalErr := json.Marshal(map[string]any{"phase": "retrieval_build",
			"snapshot_id": progress.SnapshotID, "processed_seq": progress.ProcessedSeq,
			"source_seq": progress.SourceSeq, "lexical_count": progress.LexicalCount,
			"vector_count": progress.VectorCount, "status": progress.Status})
		if marshalErr != nil {
			return marshalErr
		}
		return run.Progress.Report(ctx, event)
	})
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	outputBoundary, _ := json.Marshal(model.RetrievalBoundary{RetrievalSnapshotID: snapshot.ID,
		SourceSeq: snapshot.SourceSeq})
	return orchestrator.StepResult{Outputs: map[string]model.ResourceRef{
		"snapshot": {ResourceType: model.ResourceRetrievalSnapshot, ResourceID: snapshot.ID, Boundary: outputBoundary},
	}, Metrics: map[string]any{"status": snapshot.Status, "source_seq": snapshot.SourceSeq,
		"lexical_count": snapshot.LexicalCount, "vector_count": snapshot.VectorCount}}, nil
}

func (e *BuildExecutor) Resume(ctx context.Context, run orchestrator.StepRunContext,
	_ json.RawMessage) (orchestrator.StepResult, error) {
	return e.Execute(ctx, run)
}

func decodeBuildConfig(raw json.RawMessage) (buildConfig, error) {
	var config buildConfig
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return config, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("retrieval.build config 非法: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return config, fmt.Errorf("retrieval.build config 只能包含一个 JSON object")
	}
	config.RetrievalProfileID = strings.TrimSpace(config.RetrievalProfileID)
	return config, nil
}
