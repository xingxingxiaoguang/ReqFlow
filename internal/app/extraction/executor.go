package extraction

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

type Executor struct {
	extractions *Service
}

func NewExecutor(extractions *Service) (*Executor, error) {
	if extractions == nil {
		return nil, fmt.Errorf("document.extract: extraction service is required")
	}
	return &Executor{extractions: extractions}, nil
}

func (*Executor) Kind() model.StepKind { return model.StepKindDocumentExtract }

type extractConfig struct {
	ExtractionProfileID string `json:"extraction_profile_id"`
}

func (e *Executor) ValidateDefinition(ctx context.Context, step model.StepDefinition) error {
	if len(step.Inputs) != 1 || strings.TrimSpace(step.Inputs["documents"]) == "" {
		return fmt.Errorf("document.extract 必须且只能声明 documents 输入")
	}
	if len(step.Outputs) != 1 || step.Outputs["drafts"] != model.ResourceRecordDrafts {
		return fmt.Errorf("document.extract 必须且只能声明 drafts: record_drafts 输出")
	}
	config, err := decodeExtractConfig(step.Config)
	if err != nil {
		return err
	}
	if _, err := uuid.Parse(config.ExtractionProfileID); err != nil {
		return fmt.Errorf("extraction_profile_id 必须是 UUID")
	}
	if _, err := e.extractions.GetProfile(ctx, config.ExtractionProfileID); err != nil {
		return fmt.Errorf("ExtractionProfile 不存在: %w", err)
	}
	return nil
}

func (e *Executor) Execute(ctx context.Context, run orchestrator.StepRunContext) (orchestrator.StepResult, error) {
	return e.run(ctx, run, nil)
}

func (e *Executor) run(ctx context.Context, run orchestrator.StepRunContext,
	checkpoint json.RawMessage) (orchestrator.StepResult, error) {
	input, ok := run.Inputs["documents"]
	if !ok || input.ResourceType != model.ResourceParsedDocuments || strings.TrimSpace(input.ResourceID) == "" {
		return orchestrator.StepResult{}, fmt.Errorf("document.extract documents 输入必须是具体 ParsedDocumentSet")
	}
	config, err := decodeExtractConfig(run.Config)
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	manifest, err := e.extractions.Extract(ctx, ExtractInput{ParsedDocumentSetID: input.ResourceID,
		ExtractionProfileID: config.ExtractionProfileID, SourceStepRunID: run.StepRunID,
		ProducerAttempt: run.Attempt, Checkpoint: checkpoint,
		SaveCheckpoint: func(raw json.RawMessage) error {
			return run.Checkpoint.Save(ctx, raw)
		}}, func(progress Progress) error {
		event, marshalErr := json.Marshal(map[string]any{
			"phase": "extracting", "unit_key": progress.UnitKey, "ordinal": progress.Ordinal,
			"total": progress.Total, "completed": progress.Completed,
			"succeeded": progress.Succeeded, "failed": progress.Failed,
			"draft_count": progress.DraftCount, "status": progress.Status,
		})
		if marshalErr != nil {
			return marshalErr
		}
		return run.Progress.Report(ctx, event)
	})
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	// Manifest 先落终态，再让步骤失败。这样失败详情和已成功单元都可审计，用户重试
	// 同一 StepRun 时新 attempt 只会重跑失败单元，不会把不完整草稿绑定给下游。
	if manifest.Status != model.RecordDraftSetSucceeded {
		return orchestrator.StepResult{}, fmt.Errorf("document.extract 产出 %s Manifest %s（成功单元 %d，失败单元 %d）",
			manifest.Status, manifest.ID, manifest.SucceededUnitCount, manifest.FailedUnitCount)
	}
	profile, err := e.extractions.GetProfile(ctx, manifest.ExtractionProfileID)
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	boundary, _ := json.Marshal(model.RecordDraftsBoundary{
		ParsedDocumentSetID: manifest.ParsedDocumentSetID, ExtractionProfileID: profile.ID,
		TargetSchemaID: profile.TargetSchemaID, ProfileHash: profile.ProfileHash, Model: manifest.Model,
	})
	return orchestrator.StepResult{Outputs: map[string]model.ResourceRef{
		"drafts": {ResourceType: model.ResourceRecordDrafts, ResourceID: manifest.ID, Boundary: boundary},
	}, Metrics: map[string]any{
		"status": manifest.Status, "units": manifest.UnitCount,
		"succeeded_units": manifest.SucceededUnitCount,
		"failed_units":    manifest.FailedUnitCount, "drafts": manifest.DraftCount,
		"llm_requests": manifest.LLMRequestCount, "input_tokens": manifest.InputTokens,
		"output_tokens": manifest.OutputTokens, "cache_read_tokens": manifest.CacheReadTokens,
		"cache_write_tokens": manifest.CacheWriteTokens,
	}}, nil
}

func (e *Executor) Resume(ctx context.Context, run orchestrator.StepRunContext,
	checkpoint json.RawMessage) (orchestrator.StepResult, error) {
	return e.run(ctx, run, checkpoint)
}

func decodeExtractConfig(raw json.RawMessage) (extractConfig, error) {
	var config extractConfig
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return config, fmt.Errorf("document.extract config 必须包含 extraction_profile_id")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("document.extract config 非法: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return config, fmt.Errorf("document.extract config 只能包含一个 JSON object")
	}
	config.ExtractionProfileID = strings.TrimSpace(config.ExtractionProfileID)
	if config.ExtractionProfileID == "" {
		return config, fmt.Errorf("extraction_profile_id 不能为空")
	}
	return config, nil
}
