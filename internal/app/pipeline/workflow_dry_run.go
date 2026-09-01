package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

// source.parse dry-run：读取正式 AssetSet（只读），用真实解析器在内存中
// 解析每个资产；输出只进入预览样本，不写 parsed_documents。
type WorkflowSourceParseDryRunner struct {
	assets *AssetService
}

func NewWorkflowSourceParseDryRunner(assets *AssetService) (*WorkflowSourceParseDryRunner, error) {
	if assets == nil {
		return nil, fmt.Errorf("workflow source.parse dry-run: asset service is required")
	}
	return &WorkflowSourceParseDryRunner{assets: assets}, nil
}

func (*WorkflowSourceParseDryRunner) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "source.parse", Version: 1}
}

func (e *WorkflowSourceParseDryRunner) DryRun(ctx context.Context, execution port.WorkflowDryRunExecution) (port.WorkflowDryRunResult, error) {
	input, ok := workflowInput(execution.Inputs, "assets")
	if !ok || input.ResourceType != domain.ResourceAssetSet || strings.TrimSpace(input.ResourceID) == "" {
		return port.WorkflowDryRunResult{}, fmt.Errorf("source.parse 预览需要 assets 输入引用具体 AssetSet")
	}
	if _, err := e.assets.repo.GetAssetSet(ctx, input.ResourceID); err != nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("读取 AssetSet: %w", err)
	}
	entries, err := e.assets.repo.ListAssetSetEntries(ctx, input.ResourceID)
	if err != nil {
		return port.WorkflowDryRunResult{}, err
	}
	if len(entries) == 0 {
		return port.WorkflowDryRunResult{}, fmt.Errorf("AssetSet 不包含可解析资产")
	}
	documents := make([]map[string]any, 0, len(entries))
	succeeded, failed := 0, 0
	for _, entry := range entries {
		document := map[string]any{"asset_id": entry.Asset.ID, "file_name": entry.Asset.Filename,
			"parser_name": e.assets.parser.ParserName(), "parser_version": e.assets.parser.ParserVersion()}
		blocks, parseErr := e.parseInMemory(ctx, entry.Asset)
		if parseErr != nil {
			failed++
			document["status"], document["error"] = "failed", parseErr.Error()
		} else {
			succeeded++
			blockSamples := make([]map[string]any, 0, len(blocks))
			for _, block := range blocks {
				blockSamples = append(blockSamples, map[string]any{"ordinal": block.Ordinal,
					"block_type": block.BlockType, "page_no": block.PageNo,
					"section_path": block.SectionPath, "text": block.Text})
			}
			document["status"], document["block_count"] = "succeeded", len(blocks)
			document["blocks"] = blockSamples
		}
		documents = append(documents, document)
	}
	if succeeded == 0 {
		return port.WorkflowDryRunResult{}, fmt.Errorf("AssetSet 预览解析全部失败（%d 个资产）", failed)
	}
	sample, err := json.Marshal(map[string]any{"documents": documents})
	if err != nil {
		return port.WorkflowDryRunResult{}, err
	}
	return port.WorkflowDryRunResult{
		Outputs: []domain.NodeResourceBinding{{Port: "documents",
			ResourceType: domain.ResourceParsedDocuments,
			ResourceID:   domain.TemporaryResourceID(execution.PreviewID, execution.Node.ID, "documents")}},
		Samples: map[string]json.RawMessage{"documents": sample},
		Metrics: map[string]any{"assets": len(entries), "succeeded": succeeded, "failed": failed},
	}, nil
}

func (e *WorkflowSourceParseDryRunner) parseInMemory(ctx context.Context, asset model.Asset) ([]model.DocumentBlock, error) {
	reader, err := e.assets.blobs.Open(ctx, asset.BlobURI)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return e.assets.parser.Parse(ctx, port.ParseSource{Filename: asset.Filename,
		MIMEType: asset.MIMEType, SizeBytes: asset.SizeBytes, Content: reader}, nil)
}

// data.transform dry-run：对上游样本草稿运行真实确定性清洗内核。
type WorkflowDataTransformDryRunner struct{}

func NewWorkflowDataTransformDryRunner() (*WorkflowDataTransformDryRunner, error) {
	return &WorkflowDataTransformDryRunner{}, nil
}

func (*WorkflowDataTransformDryRunner) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "data.transform", Version: 1}
}

type draftSample struct {
	Records []struct {
		Fields map[string]json.RawMessage `json:"fields"`
	} `json:"records"`
}

func (WorkflowDataTransformDryRunner) DryRun(_ context.Context, execution port.WorkflowDryRunExecution) (port.WorkflowDryRunResult, error) {
	rules, err := compileDryRunContract(execution.Rules.DataContract)
	if err != nil {
		return port.WorkflowDryRunResult{}, err
	}
	upstream, ok := execution.Samples.Upstream("drafts")
	if !ok {
		return port.WorkflowDryRunResult{}, fmt.Errorf("data.transform 预览缺少上游 drafts 样本")
	}
	var sample draftSample
	if err := json.Unmarshal(upstream.Payload, &sample); err != nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("drafts 样本必须是 {\"records\":[...]}: %w", err)
	}
	if len(sample.Records) == 0 {
		return port.WorkflowDryRunResult{}, fmt.Errorf("drafts 样本至少包含一条记录")
	}
	var normalization []domain.NormalizationRule
	if execution.Rules.Extraction != nil {
		normalization = execution.Rules.Extraction.NormalizationRules
	}
	records := make([]map[string]any, 0, len(sample.Records))
	changed, issueCount := 0, 0
	for i, record := range sample.Records {
		fields, err := json.Marshal(record.Fields)
		if err != nil {
			return port.WorkflowDryRunResult{}, fmt.Errorf("第 %d 条样本字段非法: %w", i+1, err)
		}
		if _, err := logic.NormalizeDatasetItem(rules.schema, fields); err != nil {
			return port.WorkflowDryRunResult{}, fmt.Errorf("第 %d 条样本不符合 DataContract: %w", i+1, err)
		}
		transformed, changes, issues, transformErr := logic.TransformRecord(rules.schema, normalization, fields)
		if transformErr != nil {
			return port.WorkflowDryRunResult{}, fmt.Errorf("清洗第 %d 条样本: %w", i+1, transformErr)
		}
		if len(changes) > 0 {
			changed++
		}
		issueCount += len(issues)
		records = append(records, map[string]any{"ordinal": i, "fields": json.RawMessage(transformed),
			"changes": changes, "issues": issues})
	}
	payload, err := json.Marshal(map[string]any{"records": records})
	if err != nil {
		return port.WorkflowDryRunResult{}, err
	}
	return port.WorkflowDryRunResult{
		Outputs: []domain.NodeResourceBinding{{Port: "records",
			ResourceType: domain.ResourceTransformedRecords,
			ResourceID:   domain.TemporaryResourceID(execution.PreviewID, execution.Node.ID, "records")}},
		Samples: map[string]json.RawMessage{"records": payload},
		Metrics: map[string]any{"records": len(records), "changed_records": changed, "issues": issueCount},
	}, nil
}

type dryRunContract struct {
	schema     json.RawMessage
	schemaHash string
	contract   domain.DataContract
}

func compileDryRunContract(contract *domain.DataContract) (*dryRunContract, error) {
	if contract == nil {
		return nil, fmt.Errorf("预览需要内联 DataContract")
	}
	schema, schemaHash, err := domain.CompileDataContract(*contract)
	if err != nil {
		return nil, fmt.Errorf("编译 DataContract: %w", err)
	}
	return &dryRunContract{schema: schema, schemaHash: schemaHash, contract: *contract}, nil
}

// data.validate dry-run：对上游样本运行真实校验内核，并对照正式目标
// Dataset 的 Schema、key_fields 与已提交条目做只读冲突检查。
type WorkflowDataValidateDryRunner struct {
	cleaning *CleaningService
}

func NewWorkflowDataValidateDryRunner(cleaning *CleaningService) (*WorkflowDataValidateDryRunner, error) {
	if cleaning == nil {
		return nil, fmt.Errorf("workflow data.validate dry-run: cleaning service is required")
	}
	return &WorkflowDataValidateDryRunner{cleaning: cleaning}, nil
}

func (*WorkflowDataValidateDryRunner) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "data.validate", Version: 1}
}

type transformedSample struct {
	Records []struct {
		Fields json.RawMessage     `json:"fields"`
		Issues []model.RecordIssue `json:"issues"`
	} `json:"records"`
}

func (e *WorkflowDataValidateDryRunner) DryRun(ctx context.Context, execution port.WorkflowDryRunExecution) (port.WorkflowDryRunResult, error) {
	contract, err := compileDryRunContract(execution.Rules.DataContract)
	if err != nil {
		return port.WorkflowDryRunResult{}, err
	}
	datasetInput, ok := workflowInput(execution.Inputs, "dataset")
	if !ok || datasetInput.ResourceType != domain.ResourceDataset || strings.TrimSpace(datasetInput.ResourceID) == "" {
		return port.WorkflowDryRunResult{}, fmt.Errorf("data.validate 预览需要 dataset 输入引用具体 Dataset")
	}
	upstream, ok := execution.Samples.Upstream("records")
	if !ok {
		return port.WorkflowDryRunResult{}, fmt.Errorf("data.validate 预览缺少上游 records 样本")
	}
	var sample transformedSample
	if err := json.Unmarshal(upstream.Payload, &sample); err != nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("records 样本必须是 {\"records\":[...]}: %w", err)
	}
	if len(sample.Records) == 0 {
		return port.WorkflowDryRunResult{}, fmt.Errorf("records 样本至少包含一条记录")
	}
	dataset, err := e.cleaning.repo.GetAppendDataset(ctx, datasetInput.ResourceID)
	if err != nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("读取目标 Dataset: %w", err)
	}
	if dataset.Status != model.DatasetStatusActive {
		return port.WorkflowDryRunResult{}, fmt.Errorf("目标 Dataset %s 当前状态 %s 不允许校验追加", dataset.ID, dataset.Status)
	}
	schema, err := e.cleaning.repo.GetDatasetSchema(ctx, dataset.SchemaID)
	if err != nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("读取目标 DatasetSchema: %w", err)
	}
	if schema.SchemaHash != contract.schemaHash || !slices.Equal(dataset.KeyFields, contract.contract.KeyFields) {
		return port.WorkflowDryRunResult{}, fmt.Errorf("目标 Dataset 与内联 DataContract 不一致，预览终止")
	}
	var validationRules []domain.ValidationRule
	if execution.Rules.Extraction != nil {
		validationRules = execution.Rules.Extraction.ValidationRules
	}
	keyCounts := map[string]int{}
	keys := make([]string, 0, len(sample.Records))
	type validatePlan struct {
		ordinal int
		fields  json.RawMessage
		itemKey string
		issues  []model.RecordIssue
	}
	plans := make([]validatePlan, 0, len(sample.Records))
	counts := map[string]int{model.ValidationRecordValid: 0, model.ValidationRecordWarning: 0,
		model.ValidationRecordInvalid: 0, model.ValidationRecordDuplicate: 0, model.ValidationRecordConflict: 0}
	for i, record := range sample.Records {
		fields, issues, validateErr := logic.ValidateTransformedRecord(schema.JSONSchema, validationRules, record.Fields)
		if validateErr != nil {
			return port.WorkflowDryRunResult{}, fmt.Errorf("校验第 %d 条样本: %w", i+1, validateErr)
		}
		merged := make([]model.RecordIssue, 0, len(record.Issues)+len(issues))
		merged = append(merged, record.Issues...)
		merged = append(merged, issues...)
		plan := validatePlan{ordinal: i, fields: fields, issues: merged}
		if !hasErrorIssues(merged) {
			itemKey, _, identityErr := logic.DatasetItemIdentity(schema.SchemaHash, dataset.KeyFields, fields)
			if identityErr != nil {
				plan.issues = append(plan.issues, model.RecordIssue{Code: "identity_invalid",
					Severity: model.RecordIssueError, Message: identityErr.Error()})
			} else {
				plan.itemKey = itemKey
				keyCounts[itemKey]++
				keys = append(keys, itemKey)
			}
		}
		plans = append(plans, plan)
	}
	existingKeys, err := e.cleaning.repo.FindExistingDatasetItemKeys(ctx, dataset.ID, dataset.CurrentSeq, keys)
	if err != nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("查询已有 Dataset ItemKey: %w", err)
	}
	results := make([]map[string]any, 0, len(plans))
	for i := range plans {
		plan := &plans[i]
		duplicate := plan.itemKey != "" && keyCounts[plan.itemKey] > 1
		_, conflict := existingKeys[plan.itemKey]
		if duplicate {
			plan.issues = append(plan.issues, model.RecordIssue{Code: "duplicate_in_batch",
				Severity: model.RecordIssueError, Message: "同一待发布 Batch 内存在重复业务主键"})
		}
		if conflict && plan.itemKey != "" {
			plan.issues = append(plan.issues, model.RecordIssue{Code: "conflict_existing_key",
				Severity: model.RecordIssueError, Message: "业务主键与目标 Dataset 已提交条目冲突"})
		}
		status := model.ValidationRecordValid
		switch {
		case hasErrorIssues(plan.issues):
			// 转换/Schema/业务规则错误优先于 key 分类；duplicate/conflict 问题仍保留。
			if duplicate {
				status = model.ValidationRecordDuplicate
			} else if conflict {
				status = model.ValidationRecordConflict
			} else {
				status = model.ValidationRecordInvalid
			}
		case hasWarningIssues(plan.issues):
			status = model.ValidationRecordWarning
		}
		counts[status]++
		results = append(results, map[string]any{"ordinal": plan.ordinal, "fields": plan.fields,
			"item_key": plan.itemKey, "status": status, "issues": plan.issues})
	}
	payload, err := json.Marshal(map[string]any{"results": results, "dataset_id": dataset.ID,
		"target_schema_id": schema.ID, "validated_through_seq": dataset.CurrentSeq})
	if err != nil {
		return port.WorkflowDryRunResult{}, err
	}
	metrics := map[string]any{"records": len(results), "valid": counts[model.ValidationRecordValid],
		"warning": counts[model.ValidationRecordWarning], "invalid": counts[model.ValidationRecordInvalid],
		"duplicate": counts[model.ValidationRecordDuplicate], "conflict": counts[model.ValidationRecordConflict]}
	return port.WorkflowDryRunResult{
		Outputs: []domain.NodeResourceBinding{{Port: "validation",
			ResourceType: domain.ResourceValidationResults,
			ResourceID:   domain.TemporaryResourceID(execution.PreviewID, execution.Node.ID, "validation")}},
		Samples: map[string]json.RawMessage{"validation": payload}, Metrics: metrics,
	}, nil
}

// human.review_records dry-run：人工节点不接受伪造的成功，必须由预览
// input.samples 显式提供审核后的记录样本，并逐条通过 DataContract 校验。
type WorkflowReviewRecordsDryRunner struct{}

func NewWorkflowReviewRecordsDryRunner() (*WorkflowReviewRecordsDryRunner, error) {
	return &WorkflowReviewRecordsDryRunner{}, nil
}

func (*WorkflowReviewRecordsDryRunner) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "human.review_records", Version: 1}
}

type approvedSample struct {
	DatasetID string `json:"dataset_id"`
	Rationale string `json:"rationale"`
	Records   []struct {
		Fields json.RawMessage `json:"fields"`
	} `json:"records"`
}

func (WorkflowReviewRecordsDryRunner) DryRun(_ context.Context, execution port.WorkflowDryRunExecution) (port.WorkflowDryRunResult, error) {
	// 审核结论完全由显式样本表达；validation 输入允许来自上游样本或正式
	// ValidationResultSet 引用（链头场景），预览不读取其内容。
	raw, ok := execution.Samples.Explicit("approved")
	if !ok {
		return port.WorkflowDryRunResult{}, fmt.Errorf("human.review_records 预览需要 input.samples 提供显式审核样本 approved")
	}
	contract, err := compileDryRunContract(execution.Rules.DataContract)
	if err != nil {
		return port.WorkflowDryRunResult{}, err
	}
	var sample approvedSample
	if err := json.Unmarshal(raw, &sample); err != nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("审核样本必须是 {\"dataset_id\",\"rationale\",\"records\"}: %w", err)
	}
	if strings.TrimSpace(sample.Rationale) == "" {
		return port.WorkflowDryRunResult{}, fmt.Errorf("审核样本必须提供 rationale")
	}
	if len(sample.Records) == 0 {
		return port.WorkflowDryRunResult{}, fmt.Errorf("审核样本至少包含一条通过记录")
	}
	fields := make([]json.RawMessage, 0, len(sample.Records))
	for i, record := range sample.Records {
		normalized, err := logic.NormalizeDatasetItem(contract.schema, record.Fields)
		if err != nil {
			return port.WorkflowDryRunResult{}, fmt.Errorf("第 %d 条审核记录不符合 DataContract: %w", i+1, err)
		}
		fields = append(fields, normalized)
	}
	payload, err := json.Marshal(map[string]any{"dataset_id": strings.TrimSpace(sample.DatasetID),
		"rationale": strings.TrimSpace(sample.Rationale), "records": fields})
	if err != nil {
		return port.WorkflowDryRunResult{}, err
	}
	return port.WorkflowDryRunResult{
		Simulated: true,
		Outputs: []domain.NodeResourceBinding{{Port: "approved",
			ResourceType: domain.ResourceApprovedRecords,
			ResourceID:   domain.TemporaryResourceID(execution.PreviewID, execution.Node.ID, "approved")}},
		Samples: map[string]json.RawMessage{"approved": payload},
		Metrics: map[string]any{"records": len(fields)},
	}, nil
}

// data.publish dry-run：副作用 Capability 的专用 dry-run。用真实
// NextCommitRange 计算批次位点，但不写 Dataset/Batch，输出标记模拟。
type WorkflowDataPublishDryRunner struct {
	cleaning *CleaningService
}

func NewWorkflowDataPublishDryRunner(cleaning *CleaningService) (*WorkflowDataPublishDryRunner, error) {
	if cleaning == nil {
		return nil, fmt.Errorf("workflow data.publish dry-run: cleaning service is required")
	}
	return &WorkflowDataPublishDryRunner{cleaning: cleaning}, nil
}

func (*WorkflowDataPublishDryRunner) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "data.publish", Version: 1}
}

func (e *WorkflowDataPublishDryRunner) DryRun(ctx context.Context, execution port.WorkflowDryRunExecution) (port.WorkflowDryRunResult, error) {
	upstream, ok := execution.Samples.Upstream("approved")
	if !ok {
		return port.WorkflowDryRunResult{}, fmt.Errorf("data.publish 预览缺少上游 approved 样本")
	}
	var sample approvedSample
	if err := json.Unmarshal(upstream.Payload, &sample); err != nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("approved 样本必须是 {\"dataset_id\",\"records\"}: %w", err)
	}
	if len(sample.Records) == 0 {
		return port.WorkflowDryRunResult{}, fmt.Errorf("approved 样本不能为空批次")
	}
	if strings.TrimSpace(sample.DatasetID) == "" {
		return port.WorkflowDryRunResult{}, fmt.Errorf("approved 样本缺少 dataset_id")
	}
	dataset, err := e.cleaning.repo.GetAppendDataset(ctx, sample.DatasetID)
	if err != nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("读取目标 Dataset: %w", err)
	}
	if dataset.Status != model.DatasetStatusActive {
		return port.WorkflowDryRunResult{}, fmt.Errorf("目标 Dataset %s 当前状态 %s 不允许追加发布", dataset.ID, dataset.Status)
	}
	from, to, err := logic.NextCommitRange(dataset.CurrentSeq, len(sample.Records))
	if err != nil {
		return port.WorkflowDryRunResult{}, err
	}
	boundary, err := json.Marshal(map[string]any{"dataset_id": dataset.ID, "through_seq": to, "simulated": true})
	if err != nil {
		return port.WorkflowDryRunResult{}, err
	}
	batch, err := json.Marshal(map[string]any{"dataset_id": dataset.ID, "from_seq": from,
		"to_seq": to, "item_count": len(sample.Records), "simulated": true})
	if err != nil {
		return port.WorkflowDryRunResult{}, err
	}
	return port.WorkflowDryRunResult{
		Simulated: true,
		Outputs: []domain.NodeResourceBinding{
			{Port: "dataset", ResourceType: domain.ResourceDatasetBoundary, ResourceID: dataset.ID, Boundary: boundary},
			{Port: "batch", ResourceType: domain.ResourceDatasetBatch,
				ResourceID: domain.TemporaryResourceID(execution.PreviewID, execution.Node.ID, "batch"), Boundary: batch},
		},
		Samples: map[string]json.RawMessage{"dataset": boundary, "batch": batch},
		Metrics: map[string]any{"items": len(sample.Records), "from_seq": from, "to_seq": to},
	}, nil
}
