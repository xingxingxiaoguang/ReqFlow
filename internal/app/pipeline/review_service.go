package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"reqflow/internal/app/orchestrator"
	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

const (
	maxReviewerLength   = 200
	maxRationaleLength  = 2000
	maxReviewNoteLength = 1000
)

type HumanReviewRuntime interface {
	GetHumanStepContext(ctx context.Context, taskID, stepID string) (*orchestrator.HumanStepContext, error)
	ApproveHumanStep(ctx context.Context, taskID, stepID string, result orchestrator.StepResult) error
}

type ReviewService struct {
	repo    port.ReviewPipelineRepo
	runtime HumanReviewRuntime
}

func NewReviewService(repo port.ReviewPipelineRepo, runtime HumanReviewRuntime) (*ReviewService, error) {
	if repo == nil {
		return nil, fmt.Errorf("review pipeline: repo is required")
	}
	if runtime == nil {
		return nil, fmt.Errorf("review pipeline: runtime is required")
	}
	return &ReviewService{repo: repo, runtime: runtime}, nil
}

type ReviewDecisionInput struct {
	ValidationResultID string          `json:"validation_result_id"`
	Action             string          `json:"action"`
	Fields             json.RawMessage `json:"fields,omitempty"`
	Note               string          `json:"note,omitempty"`
}

type ReviewRecordsInput struct {
	Reviewer  string                `json:"reviewer"`
	Rationale string                `json:"rationale"`
	Decisions []ReviewDecisionInput `json:"decisions"`
}

func (s *ReviewService) Review(ctx context.Context, taskID, stepID string, input ReviewRecordsInput) (*model.ApprovedRecordSet, error) {
	human, err := s.runtime.GetHumanStepContext(ctx, strings.TrimSpace(taskID), strings.TrimSpace(stepID))
	if err != nil {
		return nil, err
	}
	validationRef, allowEdit, err := validateReviewStepContract(human.Definition, human.Inputs)
	if err != nil {
		return nil, err
	}
	input, reviewHash, err := normalizeReviewInput(validationRef.ResourceID, input)
	if err != nil {
		return nil, err
	}
	if existing, found, err := s.repo.FindApprovedRecordSetByStepRun(ctx, human.Run.ID); err != nil {
		return nil, err
	} else if found {
		if existing.ReviewHash != reviewHash {
			return nil, fmt.Errorf("人工 Gate 已由不同审核内容完成")
		}
		if err := s.completeReviewStep(ctx, taskID, stepID, existing); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if human.Run.Status != model.StepRunAwaiting {
		return nil, fmt.Errorf("%w: 人工 Gate 当前状态为 %s", port.ErrInvalidTransition, human.Run.Status)
	}

	validationSet, err := s.repo.GetValidationResultSet(ctx, validationRef.ResourceID)
	if err != nil {
		return nil, fmt.Errorf("读取 ValidationResultSet: %w", err)
	}
	results, err := s.repo.ListValidationResults(ctx, validationSet.ID)
	if err != nil {
		return nil, fmt.Errorf("读取 ValidationResult: %w", err)
	}
	if validationSet.Status != model.ValidationResultSetSucceeded || len(results) != validationSet.RecordCount {
		return nil, fmt.Errorf("ValidationResultSet %s 尚未完整完成", validationSet.ID)
	}
	dataset, err := s.repo.GetAppendDataset(ctx, validationSet.TargetDatasetID)
	if err != nil {
		return nil, fmt.Errorf("读取目标 Dataset: %w", err)
	}
	if dataset.Status != model.DatasetStatusActive || dataset.SchemaID != validationSet.TargetSchemaID {
		return nil, fmt.Errorf("目标 Dataset 状态或 Schema 已不再匹配审核输入")
	}
	schema, err := s.repo.GetDatasetSchema(ctx, dataset.SchemaID)
	if err != nil {
		return nil, fmt.Errorf("读取目标 DatasetSchema: %w", err)
	}
	transformedSet, err := s.repo.GetTransformedRecordSet(ctx, validationSet.TransformedRecordSetID)
	if err != nil {
		return nil, fmt.Errorf("读取 TransformedRecordSet: %w", err)
	}
	profile, err := s.repo.GetExtractionProfile(ctx, transformedSet.ExtractionProfileID)
	if err != nil {
		return nil, fmt.Errorf("读取 ExtractionProfile: %w", err)
	}
	provenance, err := s.reviewProvenance(ctx, transformedSet)
	if err != nil {
		return nil, err
	}

	inputByResult := make(map[string]ReviewDecisionInput, len(input.Decisions))
	for _, decision := range input.Decisions {
		inputByResult[decision.ValidationResultID] = decision
	}
	if len(inputByResult) != len(results) {
		return nil, fmt.Errorf("审核必须且只能覆盖 ValidationResultSet 的全部 %d 条记录", len(results))
	}

	decisions := make([]model.RecordReviewDecision, 0, len(results))
	selectedKeys := make([]string, 0, len(results))
	seenKeys := make(map[string]string, len(results))
	set := &model.ApprovedRecordSet{ValidationResultSetID: validationSet.ID,
		TargetDatasetID: dataset.ID, TargetSchemaID: schema.ID, SourceStepRunID: human.Run.ID,
		Reviewer: input.Reviewer, Rationale: input.Rationale, ReviewHash: reviewHash,
		ReviewedThroughSeq: dataset.CurrentSeq, RecordCount: len(results)}
	for _, result := range results {
		request, ok := inputByResult[result.ID]
		if !ok {
			return nil, fmt.Errorf("审核缺少 ValidationResult %s", result.ID)
		}
		decision := model.RecordReviewDecision{ValidationResultID: result.ID,
			TransformedRecordID: result.TransformedRecordID, Ordinal: result.Ordinal,
			Action: request.Action, Note: request.Note, Provenance: provenance[result.TransformedRecordID]}
		switch request.Action {
		case model.ReviewActionApprove:
			if result.Status != model.ValidationRecordValid && result.Status != model.ValidationRecordWarning {
				return nil, fmt.Errorf("第 %d 条状态为 %s，必须编辑修复或排除", result.Ordinal+1, result.Status)
			}
			if hasErrorIssues(result.Issues) {
				return nil, fmt.Errorf("第 %d 条仍有错误，不能直接批准", result.Ordinal+1)
			}
			decision.Fields, decision.ItemKey, decision.Fingerprint = result.Fields, result.ItemKey, result.Fingerprint
			decision.Issues = append([]model.RecordIssue(nil), result.Issues...)
			set.ApprovedCount++
		case model.ReviewActionEdit:
			if !allowEdit {
				return nil, fmt.Errorf("步骤 %s 不允许编辑记录", stepID)
			}
			fields, issues, validateErr := logic.ValidateTransformedRecord(schema.JSONSchema,
				profile.ValidationRules, request.Fields)
			if validateErr != nil {
				return nil, fmt.Errorf("第 %d 条编辑结果非法: %w", result.Ordinal+1, validateErr)
			}
			if hasErrorIssues(issues) {
				return nil, fmt.Errorf("第 %d 条编辑结果仍未通过校验", result.Ordinal+1)
			}
			itemKey, fingerprint, identityErr := logic.DatasetItemIdentity(schema.SchemaHash, dataset.KeyFields, fields)
			if identityErr != nil {
				return nil, fmt.Errorf("第 %d 条编辑结果主键非法: %w", result.Ordinal+1, identityErr)
			}
			decision.Fields, decision.ItemKey, decision.Fingerprint = fields, itemKey, fingerprint
			decision.Issues = issues
			set.EditedCount++
		case model.ReviewActionExclude:
			decision.Fields = result.Fields
			decision.Issues = append([]model.RecordIssue(nil), result.Issues...)
			set.ExcludedCount++
		default:
			return nil, fmt.Errorf("第 %d 条审核 action 非法: %s", result.Ordinal+1, request.Action)
		}
		if decision.Action != model.ReviewActionExclude {
			if previous, duplicate := seenKeys[decision.ItemKey]; duplicate {
				return nil, fmt.Errorf("审核后的记录 %s 与 %s 业务主键重复", previous, result.ID)
			}
			seenKeys[decision.ItemKey] = result.ID
			selectedKeys = append(selectedKeys, decision.ItemKey)
		}
		decisions = append(decisions, decision)
	}
	if set.ApprovedCount+set.EditedCount == 0 {
		return nil, fmt.Errorf("审核不能排除全部记录；没有可发布内容")
	}
	existingKeys, err := s.repo.FindExistingDatasetItemKeys(ctx, dataset.ID, dataset.CurrentSeq, selectedKeys)
	if err != nil {
		return nil, fmt.Errorf("复查目标 Dataset 冲突: %w", err)
	}
	if len(existingKeys) > 0 {
		return nil, fmt.Errorf("审核结果与当前 Dataset 存在 %d 个业务主键冲突，请排除或重新生成", len(existingKeys))
	}
	stored, err := s.repo.CreateApprovedRecordSet(ctx, set, decisions)
	if err != nil {
		return nil, err
	}
	if err := s.completeReviewStep(ctx, taskID, stepID, stored); err != nil {
		return nil, err
	}
	return stored, nil
}

func (s *ReviewService) reviewProvenance(ctx context.Context, transformedSet *model.TransformedRecordSet) (map[string]model.ItemProvenance, error) {
	records, err := s.repo.ListTransformedRecords(ctx, transformedSet.ID)
	if err != nil {
		return nil, fmt.Errorf("读取转换记录: %w", err)
	}
	drafts, err := s.repo.ListRecordDrafts(ctx, transformedSet.RecordDraftSetID)
	if err != nil {
		return nil, fmt.Errorf("读取候选记录 provenance: %w", err)
	}
	draftProvenance := make(map[string]model.ItemProvenance, len(drafts))
	for _, draft := range drafts {
		draftProvenance[draft.ID] = draft.Provenance
	}
	out := make(map[string]model.ItemProvenance, len(records))
	for _, record := range records {
		value, ok := draftProvenance[record.RecordDraftID]
		if !ok {
			return nil, fmt.Errorf("TransformedRecord %s 缺少来源 RecordDraft", record.ID)
		}
		out[record.ID] = value
	}
	return out, nil
}

func (s *ReviewService) completeReviewStep(ctx context.Context, taskID, stepID string, set *model.ApprovedRecordSet) error {
	boundary, _ := json.Marshal(model.ApprovedRecordsBoundary{ValidationResultSetID: set.ValidationResultSetID,
		TargetDatasetID: set.TargetDatasetID, TargetSchemaID: set.TargetSchemaID,
		ReviewedThroughSeq: set.ReviewedThroughSeq, ReviewHash: set.ReviewHash})
	return s.runtime.ApproveHumanStep(ctx, taskID, stepID, orchestrator.StepResult{Outputs: map[string]model.ResourceRef{
		"approved": {ResourceType: model.ResourceApprovedRecords, ResourceID: set.ID, Boundary: boundary},
	}})
}

func validateReviewStepContract(step model.StepDefinition, inputs map[string]model.ResourceRef) (model.ResourceRef, bool, error) {
	if len(step.Inputs) != 1 || strings.TrimSpace(step.Inputs["validation"]) == "" ||
		len(step.Outputs) != 1 || step.Outputs["approved"] != model.ResourceApprovedRecords {
		return model.ResourceRef{}, false, fmt.Errorf("human.review 必须声明 validation 输入和 approved: approved_records 输出")
	}
	validation, ok := inputs["validation"]
	if !ok || validation.ResourceType != model.ResourceValidationResults || strings.TrimSpace(validation.ResourceID) == "" {
		return model.ResourceRef{}, false, fmt.Errorf("human.review validation 输入必须是具体 ValidationResultSet")
	}
	config := struct {
		AllowEdit *bool `json:"allow_edit"`
	}{}
	if len(bytes.TrimSpace(step.Config)) > 0 && !bytes.Equal(bytes.TrimSpace(step.Config), []byte("null")) {
		decoder := json.NewDecoder(bytes.NewReader(step.Config))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&config); err != nil {
			return model.ResourceRef{}, false, fmt.Errorf("human.review config 非法: %w", err)
		}
	}
	allowEdit := true
	if config.AllowEdit != nil {
		allowEdit = *config.AllowEdit
	}
	return validation, allowEdit, nil
}

func normalizeReviewInput(validationSetID string, input ReviewRecordsInput) (ReviewRecordsInput, string, error) {
	input.Reviewer = strings.TrimSpace(input.Reviewer)
	input.Rationale = strings.TrimSpace(input.Rationale)
	if input.Reviewer == "" || len([]rune(input.Reviewer)) > maxReviewerLength {
		return input, "", fmt.Errorf("reviewer 不能为空且不能超过 %d 个字符", maxReviewerLength)
	}
	if input.Rationale == "" || len([]rune(input.Rationale)) > maxRationaleLength {
		return input, "", fmt.Errorf("rationale 不能为空且不能超过 %d 个字符", maxRationaleLength)
	}
	if len(input.Decisions) == 0 {
		return input, "", fmt.Errorf("decisions 不能为空")
	}
	seen := make(map[string]bool, len(input.Decisions))
	for i := range input.Decisions {
		decision := &input.Decisions[i]
		decision.ValidationResultID = strings.TrimSpace(decision.ValidationResultID)
		decision.Action = strings.TrimSpace(decision.Action)
		decision.Note = strings.TrimSpace(decision.Note)
		if decision.ValidationResultID == "" || seen[decision.ValidationResultID] {
			return input, "", fmt.Errorf("decisions[%d] 的 validation_result_id 为空或重复", i)
		}
		seen[decision.ValidationResultID] = true
		if !validReviewInputAction(decision.Action) {
			return input, "", fmt.Errorf("decisions[%d] 的 action 非法", i)
		}
		if len([]rune(decision.Note)) > maxReviewNoteLength {
			return input, "", fmt.Errorf("decisions[%d] 的 note 不能超过 %d 个字符", i, maxReviewNoteLength)
		}
		if decision.Action == model.ReviewActionEdit {
			canonical, err := canonicalJSONObject(decision.Fields)
			if err != nil {
				return input, "", fmt.Errorf("decisions[%d] 的 fields 非法: %w", i, err)
			}
			decision.Fields = canonical
		} else {
			if len(bytes.TrimSpace(decision.Fields)) != 0 && !bytes.Equal(bytes.TrimSpace(decision.Fields), []byte("null")) {
				return input, "", fmt.Errorf("decisions[%d] 仅 edit action 可以提交 fields", i)
			}
			decision.Fields = nil
		}
	}
	sort.Slice(input.Decisions, func(i, j int) bool {
		return input.Decisions[i].ValidationResultID < input.Decisions[j].ValidationResultID
	})
	payload, _ := json.Marshal(struct {
		ValidationResultSetID string                `json:"validation_result_set_id"`
		Reviewer              string                `json:"reviewer"`
		Rationale             string                `json:"rationale"`
		Decisions             []ReviewDecisionInput `json:"decisions"`
	}{validationSetID, input.Reviewer, input.Rationale, input.Decisions})
	sum := sha256.Sum256(payload)
	return input, hex.EncodeToString(sum[:]), nil
}

func canonicalJSONObject(raw json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil {
		if err == nil {
			err = fmt.Errorf("根节点必须是 object")
		}
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("只能包含一个 JSON 值")
		}
		return nil, err
	}
	return json.Marshal(value)
}

func validReviewInputAction(action string) bool {
	return action == model.ReviewActionApprove || action == model.ReviewActionEdit || action == model.ReviewActionExclude
}
