package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reqflow/internal/app/orchestrator"
	"reqflow/internal/domain/model"
)

// SourceParseExecutor 把 AssetSet 解析为一等 ParsedDocumentSet Manifest。
// 逐文件失败属于资源内容，不等同于 Executor 基础设施失败。
type SourceParseExecutor struct {
	assets *AssetService
}

func NewSourceParseExecutor(assets *AssetService) (*SourceParseExecutor, error) {
	if assets == nil {
		return nil, fmt.Errorf("source.parse: asset service is required")
	}
	return &SourceParseExecutor{assets: assets}, nil
}

func (*SourceParseExecutor) Kind() model.StepKind { return model.StepKindSourceParse }

func (*SourceParseExecutor) ValidateDefinition(_ context.Context, step model.StepDefinition) error {
	if len(step.Inputs) != 1 || strings.TrimSpace(step.Inputs["assets"]) == "" {
		return fmt.Errorf("source.parse 必须且只能声明 assets 输入")
	}
	if len(step.Outputs) != 1 || step.Outputs["documents"] != model.ResourceParsedDocuments {
		return fmt.Errorf("source.parse 必须且只能声明 documents: parsed_documents 输出")
	}
	if len(step.Config) > 0 && string(step.Config) != "null" {
		var config map[string]json.RawMessage
		if err := json.Unmarshal(step.Config, &config); err != nil {
			return fmt.Errorf("config 必须是 JSON object: %w", err)
		}
		if len(config) != 0 {
			return fmt.Errorf("source.parse 当前不接受配置；Parser 版本由平台注册表固定")
		}
	}
	return nil
}

func (e *SourceParseExecutor) Execute(ctx context.Context, run orchestrator.StepRunContext) (orchestrator.StepResult, error) {
	input, ok := run.Inputs["assets"]
	if !ok || input.ResourceType != model.ResourceAssetSet || strings.TrimSpace(input.ResourceID) == "" {
		return orchestrator.StepResult{}, fmt.Errorf("source.parse assets 输入必须是具体 AssetSet")
	}
	manifest, err := e.assets.ParseAssetSet(ctx, ParseAssetSetInput{AssetSetID: input.ResourceID,
		SourceStepRunID: run.StepRunID, ProducerAttempt: run.Attempt}, func(progress ParseAssetSetProgress) error {
		checkpoint, marshalErr := json.Marshal(map[string]any{
			"manifest_id": progress.ManifestID, "completed": progress.Completed,
			"succeeded": progress.Succeeded, "failed": progress.Failed,
		})
		if marshalErr != nil {
			return marshalErr
		}
		if err := run.Checkpoint.Save(ctx, checkpoint); err != nil {
			return err
		}
		event, marshalErr := json.Marshal(map[string]any{
			"phase": "parsing", "asset_id": progress.AssetID, "ordinal": progress.Ordinal,
			"total": progress.Total, "completed": progress.Completed,
			"succeeded": progress.Succeeded, "failed": progress.Failed, "status": progress.Status,
		})
		if marshalErr != nil {
			return marshalErr
		}
		return run.Progress.Report(ctx, event)
	})
	if err != nil {
		return orchestrator.StepResult{}, err
	}
	boundary, _ := json.Marshal(model.ParsedDocumentsBoundary{AssetSetID: manifest.AssetSetID,
		ParserName: manifest.ParserName, ParserVersion: manifest.ParserVersion})
	return orchestrator.StepResult{Outputs: map[string]model.ResourceRef{
		"documents": {ResourceType: model.ResourceParsedDocuments, ResourceID: manifest.ID, Boundary: boundary},
	}, Metrics: map[string]any{
		"status": manifest.Status, "total": manifest.TotalCount,
		"succeeded": manifest.SucceededCount, "failed": manifest.FailedCount,
	}}, nil
}

func (e *SourceParseExecutor) Resume(ctx context.Context, run orchestrator.StepRunContext, _ json.RawMessage) (orchestrator.StepResult, error) {
	// Manifest 与成员状态是恢复真相源；checkpoint 仅用于 UI 快照。新 attempt 会在
	// BeginParsedDocumentSet 中保留成功项、重置失败/未完成项，并 fence 旧 attempt。
	return e.Execute(ctx, run)
}
