package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

// WorkflowReviewManualCompleter 处理 human.review_records 的人工完成：
// 读取节点真实 ValidationResultSet 输入，要求领域载荷覆盖每条记录，在
// 服务端生成不可变 ApprovedRecordSet，并返回正式资源绑定。
type WorkflowReviewManualCompleter struct {
	repo port.ReviewPipelineRepo
}

func NewWorkflowReviewManualCompleter(repo port.ReviewPipelineRepo) (*WorkflowReviewManualCompleter, error) {
	if repo == nil {
		return nil, fmt.Errorf("workflow human.review_records: review repository is required")
	}
	return &WorkflowReviewManualCompleter{repo: repo}, nil
}

func (*WorkflowReviewManualCompleter) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "human.review_records", Version: 1}
}

type reviewCompletionPayload struct {
	Rationale string                  `json:"rationale"`
	Decisions []reviewDecisionPayload `json:"decisions"`
}

type reviewDecisionPayload struct {
	ValidationResultID string          `json:"validation_result_id"`
	Action             string          `json:"action"`
	Fields             json.RawMessage `json:"fields,omitempty"`
	Note               string          `json:"note,omitempty"`
}

func (e *WorkflowReviewManualCompleter) Complete(ctx context.Context, execution port.WorkflowManualExecution) ([]domain.NodeResourceBinding, error) {
	input, ok := workflowInput(execution.Inputs, "validation")
	if !ok || input.ResourceType != domain.ResourceValidationResults || strings.TrimSpace(input.ResourceID) == "" {
		return nil, fmt.Errorf("human.review_records 输入必须是具体 ValidationResultSet")
	}
	var payload reviewCompletionPayload
	if err := json.Unmarshal(execution.Payload, &payload); err != nil {
		return nil, fmt.Errorf("人工审核载荷非法: %w", err)
	}
	if strings.TrimSpace(payload.Rationale) == "" {
		return nil, fmt.Errorf("人工审核必须提供 rationale")
	}
	set, err := e.repo.GetValidationResultSet(ctx, input.ResourceID)
	if err != nil {
		return nil, fmt.Errorf("读取 ValidationResultSet: %w", err)
	}
	if set.Status != model.ValidationResultSetSucceeded {
		return nil, fmt.Errorf("ValidationResultSet %s 状态 %s 不允许审核", set.ID, set.Status)
	}
	results, err := e.repo.ListValidationResults(ctx, set.ID)
	if err != nil {
		return nil, fmt.Errorf("读取 ValidationResult: %w", err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("ValidationResultSet %s 没有待审核记录", set.ID)
	}
	if len(payload.Decisions) != len(results) {
		return nil, fmt.Errorf("审核决定数量 %d 与校验记录数量 %d 不一致，必须覆盖每条记录",
			len(payload.Decisions), len(results))
	}
	byResult := make(map[string]model.ValidationResult, len(results))
	for _, result := range results {
		byResult[result.ID] = result
	}
	decided := make(map[string]bool, len(payload.Decisions))
	dataset, err := e.repo.GetAppendDataset(ctx, set.TargetDatasetID)
	if err != nil {
		return nil, fmt.Errorf("读取目标 Dataset: %w", err)
	}
	schema, err := e.repo.GetDatasetSchema(ctx, set.TargetSchemaID)
	if err != nil {
		return nil, fmt.Errorf("读取目标 DatasetSchema: %w", err)
	}
	decisions := make([]model.RecordReviewDecision, 0, len(results))
	approvedCount, editedCount, excludedCount := 0, 0, 0
	for _, decision := range payload.Decisions {
		result, exists := byResult[decision.ValidationResultID]
		if !exists {
			return nil, fmt.Errorf("审核决定 %s 不属于当前 ValidationResultSet", decision.ValidationResultID)
		}
		if decided[decision.ValidationResultID] {
			return nil, fmt.Errorf("审核决定 %s 重复", decision.ValidationResultID)
		}
		decided[decision.ValidationResultID] = true
		entry := model.RecordReviewDecision{ValidationResultID: result.ID,
			TransformedRecordID: result.TransformedRecordID, Ordinal: result.Ordinal,
			Issues: result.Issues, Note: strings.TrimSpace(decision.Note)}
		switch decision.Action {
		case model.ReviewActionApprove:
			entry.Action, entry.Fields = decision.Action, result.Fields
			entry.ItemKey, entry.Fingerprint = result.ItemKey, result.Fingerprint
			approvedCount++
		case model.ReviewActionEdit:
			if len(decision.Fields) == 0 {
				return nil, fmt.Errorf("编辑决定 %s 必须提供 fields", decision.ValidationResultID)
			}
			fields, normalizeErr := logic.NormalizeDatasetItem(schema.JSONSchema, decision.Fields)
			if normalizeErr != nil {
				return nil, fmt.Errorf("编辑决定 %s 字段非法: %w", decision.ValidationResultID, normalizeErr)
			}
			itemKey, fingerprint, identityErr := logic.DatasetItemIdentity(schema.SchemaHash, dataset.KeyFields, fields)
			if identityErr != nil {
				return nil, fmt.Errorf("编辑决定 %s: %w", decision.ValidationResultID, identityErr)
			}
			entry.Action, entry.Fields, entry.ItemKey, entry.Fingerprint = decision.Action, fields, itemKey, fingerprint
			editedCount++
		case model.ReviewActionExclude:
			entry.Action, entry.Fields = decision.Action, result.Fields
			excludedCount++
		default:
			return nil, fmt.Errorf("审核动作 %q 非法", decision.Action)
		}
		decisions = append(decisions, entry)
	}
	hash, err := reviewHash(set.ID, decisions)
	if err != nil {
		return nil, err
	}
	approvedSet, err := e.repo.CreateApprovedRecordSet(ctx, &model.ApprovedRecordSet{
		ValidationResultSetID: set.ID, TargetDatasetID: set.TargetDatasetID,
		TargetSchemaID: set.TargetSchemaID, ProducerNodeRunID: execution.NodeRunID,
		Reviewer: execution.Actor, Rationale: strings.TrimSpace(payload.Rationale),
		ReviewHash: hash, ReviewedThroughSeq: set.ValidatedThroughSeq, RecordCount: len(decisions),
		ApprovedCount: approvedCount, EditedCount: editedCount, ExcludedCount: excludedCount,
	}, decisions)
	if err != nil {
		return nil, err
	}
	boundary, err := json.Marshal(map[string]any{"approved_record_set_id": approvedSet.ID,
		"dataset_id": approvedSet.TargetDatasetID, "records": approvedSet.RecordCount,
		"approved": approvedSet.ApprovedCount, "edited": approvedSet.EditedCount,
		"excluded": approvedSet.ExcludedCount})
	if err != nil {
		return nil, err
	}
	return []domain.NodeResourceBinding{{Port: "approved",
		ResourceType: domain.ResourceApprovedRecords, ResourceID: approvedSet.ID, Boundary: boundary}}, nil
}

// reviewHash 以 ValidationResultSet 与全部决定为内容计算审核指纹；同一
// Gate 的网络重试必须携带完全相同的决定才能复用结论。
func reviewHash(validationResultSetID string, decisions []model.RecordReviewDecision) (string, error) {
	sorter := make([]model.RecordReviewDecision, len(decisions))
	copy(sorter, decisions)
	for i := 1; i < len(sorter); i++ {
		for j := i; j > 0 && sorter[j].Ordinal < sorter[j-1].Ordinal; j-- {
			sorter[j], sorter[j-1] = sorter[j-1], sorter[j]
		}
	}
	content := struct {
		ValidationResultSetID string                       `json:"validation_result_set_id"`
		Decisions             []model.RecordReviewDecision `json:"decisions"`
	}{ValidationResultSetID: validationResultSetID, Decisions: sorter}
	raw, err := json.Marshal(content)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
