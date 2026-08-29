package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

const (
	TransformEngineVersion  = "deterministic-transform/v1"
	ValidationEngineVersion = "deterministic-validation/v1"
)

type CleaningService struct {
	repo port.CleaningPipelineRepo
}

func NewCleaningService(repo port.CleaningPipelineRepo) (*CleaningService, error) {
	if repo == nil {
		return nil, fmt.Errorf("cleaning pipeline: repo is required")
	}
	return &CleaningService{repo: repo}, nil
}

type TransformInput struct {
	RecordDraftSetID string
	SourceStepRunID  string
	ProducerAttempt  int
}

type CleaningProgress struct {
	ManifestID string
	Ordinal    int
	Total      int
	Completed  int
	Reused     bool
	Status     string
}

func (s *CleaningService) Transform(ctx context.Context, in TransformInput, progress func(CleaningProgress) error) (*model.TransformedRecordSet, error) {
	draftSet, _, err := s.repo.GetRecordDraftSet(ctx, strings.TrimSpace(in.RecordDraftSetID))
	if err != nil {
		return nil, fmt.Errorf("读取 RecordDraftSet: %w", err)
	}
	if draftSet.Status != model.RecordDraftSetSucceeded {
		return nil, fmt.Errorf("RecordDraftSet %s 状态 %s 不允许转换", draftSet.ID, draftSet.Status)
	}
	profile, err := s.repo.GetExtractionProfile(ctx, draftSet.ExtractionProfileID)
	if err != nil {
		return nil, fmt.Errorf("读取 ExtractionProfile: %w", err)
	}
	schema, err := s.repo.GetDatasetSchema(ctx, profile.TargetSchemaID)
	if err != nil {
		return nil, fmt.Errorf("读取目标 DatasetSchema: %w", err)
	}
	drafts, err := s.repo.ListRecordDrafts(ctx, draftSet.ID)
	if err != nil {
		return nil, fmt.Errorf("读取 RecordDraft: %w", err)
	}
	if len(drafts) != draftSet.DraftCount {
		return nil, fmt.Errorf("RecordDraftSet %s 声明 %d 条草稿，实际读取 %d 条", draftSet.ID, draftSet.DraftCount, len(drafts))
	}
	manifest, err := s.repo.BeginTransformedRecordSet(ctx, &model.TransformedRecordSet{
		RecordDraftSetID: draftSet.ID, ExtractionProfileID: profile.ID,
		TargetSchemaID: schema.ID, SourceStepRunID: strings.TrimSpace(in.SourceStepRunID),
		ProducerAttempt: in.ProducerAttempt, EngineVersion: TransformEngineVersion,
		DraftCount: len(drafts),
	})
	if err != nil {
		return nil, err
	}
	if manifest.Status == model.TransformedRecordSetSucceeded {
		return manifest, nil
	}
	existing, err := s.repo.ListTransformedRecords(ctx, manifest.ID)
	if err != nil {
		return nil, err
	}
	completed := make(map[string]bool, len(existing))
	for _, record := range existing {
		completed[record.RecordDraftID] = true
	}
	completedCount := len(existing)
	for ordinal, draft := range drafts {
		reused := completed[draft.ID]
		if !reused {
			fields, changes, issues, transformErr := logic.TransformRecord(
				schema.JSONSchema, profile.NormalizationRules, draft.Fields)
			if transformErr != nil {
				return nil, fmt.Errorf("转换第 %d 条 RecordDraft: %w", ordinal+1, transformErr)
			}
			record := &model.TransformedRecord{RecordDraftID: draft.ID, Ordinal: ordinal,
				Fields: fields, Changes: changes, Issues: issues}
			if err := s.repo.SaveTransformedRecord(ctx, manifest.ID, in.ProducerAttempt, record); err != nil {
				return nil, err
			}
			completedCount++
		}
		if progress != nil {
			if err := progress(CleaningProgress{ManifestID: manifest.ID, Ordinal: ordinal,
				Total: len(drafts), Completed: completedCount, Reused: reused, Status: "transformed"}); err != nil {
				return nil, err
			}
		}
	}
	return s.repo.FinalizeTransformedRecordSet(ctx, manifest.ID, in.ProducerAttempt)
}

type ValidateInput struct {
	TransformedRecordSetID string
	TargetDatasetID        string
	SourceStepRunID        string
	ProducerAttempt        int
}

type validationPlan struct {
	record      model.TransformedRecord
	fields      json.RawMessage
	itemKey     string
	fingerprint string
	issues      []model.RecordIssue
}

func (s *CleaningService) Validate(ctx context.Context, in ValidateInput, progress func(CleaningProgress) error) (*model.ValidationResultSet, error) {
	transformedSet, err := s.repo.GetTransformedRecordSet(ctx, strings.TrimSpace(in.TransformedRecordSetID))
	if err != nil {
		return nil, fmt.Errorf("读取 TransformedRecordSet: %w", err)
	}
	if transformedSet.Status != model.TransformedRecordSetSucceeded {
		return nil, fmt.Errorf("TransformedRecordSet %s 状态 %s 不允许校验", transformedSet.ID, transformedSet.Status)
	}
	profile, err := s.repo.GetExtractionProfile(ctx, transformedSet.ExtractionProfileID)
	if err != nil {
		return nil, fmt.Errorf("读取 ExtractionProfile: %w", err)
	}
	dataset, err := s.repo.GetAppendDataset(ctx, strings.TrimSpace(in.TargetDatasetID))
	if err != nil {
		return nil, fmt.Errorf("读取目标 Dataset: %w", err)
	}
	if dataset.Status != model.DatasetStatusActive {
		return nil, fmt.Errorf("目标 Dataset %s 当前状态 %s 不允许校验追加", dataset.ID, dataset.Status)
	}
	if dataset.SchemaID != transformedSet.TargetSchemaID || profile.TargetSchemaID != dataset.SchemaID {
		return nil, fmt.Errorf("目标 Dataset Schema 与转换结果的不可变 Schema 不一致")
	}
	schema, err := s.repo.GetDatasetSchema(ctx, dataset.SchemaID)
	if err != nil {
		return nil, fmt.Errorf("读取目标 DatasetSchema: %w", err)
	}
	records, err := s.repo.ListTransformedRecords(ctx, transformedSet.ID)
	if err != nil {
		return nil, fmt.Errorf("读取 TransformedRecord: %w", err)
	}
	if len(records) != transformedSet.TransformedCount {
		return nil, fmt.Errorf("TransformedRecordSet %s 记录数量不一致", transformedSet.ID)
	}
	manifest, err := s.repo.BeginValidationResultSet(ctx, &model.ValidationResultSet{
		TransformedRecordSetID: transformedSet.ID, TargetDatasetID: dataset.ID,
		TargetSchemaID: schema.ID, SourceStepRunID: strings.TrimSpace(in.SourceStepRunID),
		ProducerAttempt: in.ProducerAttempt, EngineVersion: ValidationEngineVersion,
		ValidatedThroughSeq: dataset.CurrentSeq, RecordCount: len(records),
	})
	if err != nil {
		return nil, err
	}
	if manifest.Status == model.ValidationResultSetSucceeded {
		return manifest, nil
	}

	plans := make([]validationPlan, len(records))
	keyCounts := make(map[string]int, len(records))
	keys := make([]string, 0, len(records))
	for i, record := range records {
		fields, issues, validateErr := logic.ValidateTransformedRecord(schema.JSONSchema, profile.ValidationRules, record.Fields)
		if validateErr != nil {
			return nil, fmt.Errorf("校验第 %d 条 TransformedRecord: %w", i+1, validateErr)
		}
		issues = append(append([]model.RecordIssue(nil), record.Issues...), issues...)
		plan := validationPlan{record: record, fields: fields, issues: issues}
		if !hasErrorIssues(issues) {
			plan.itemKey, plan.fingerprint, err = logic.DatasetItemIdentity(schema.SchemaHash, dataset.KeyFields, fields)
			if err != nil {
				plan.issues = append(plan.issues, model.RecordIssue{Code: "identity_invalid",
					Severity: model.RecordIssueError, Message: err.Error()})
			} else {
				keyCounts[plan.itemKey]++
				keys = append(keys, plan.itemKey)
			}
		}
		plans[i] = plan
	}
	existingKeys, err := s.repo.FindExistingDatasetItemKeys(ctx, dataset.ID, manifest.ValidatedThroughSeq, keys)
	if err != nil {
		return nil, fmt.Errorf("查询已有 Dataset ItemKey: %w", err)
	}
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
		result := &model.ValidationResult{TransformedRecordID: plan.record.ID,
			Ordinal: plan.record.Ordinal, Fields: plan.fields, ItemKey: plan.itemKey,
			Fingerprint: plan.fingerprint, Status: status, Issues: plan.issues}
		if err := s.repo.SaveValidationResult(ctx, manifest.ID, in.ProducerAttempt, result); err != nil {
			return nil, err
		}
		if progress != nil {
			if err := progress(CleaningProgress{ManifestID: manifest.ID, Ordinal: plan.record.Ordinal,
				Total: len(plans), Completed: i + 1, Status: status}); err != nil {
				return nil, err
			}
		}
	}
	return s.repo.FinalizeValidationResultSet(ctx, manifest.ID, in.ProducerAttempt)
}

func (s *CleaningService) GetTransformedRecordSet(ctx context.Context, id string) (*model.TransformedRecordSet, []model.TransformedRecord, error) {
	set, err := s.repo.GetTransformedRecordSet(ctx, strings.TrimSpace(id))
	if err != nil {
		return nil, nil, err
	}
	records, err := s.repo.ListTransformedRecords(ctx, set.ID)
	return set, records, err
}

func (s *CleaningService) GetValidationResultSet(ctx context.Context, id string) (*model.ValidationResultSet, []model.ValidationResult, error) {
	set, err := s.repo.GetValidationResultSet(ctx, strings.TrimSpace(id))
	if err != nil {
		return nil, nil, err
	}
	results, err := s.repo.ListValidationResults(ctx, set.ID)
	return set, results, err
}

func (s *CleaningService) GetProfile(ctx context.Context, id string) (*model.ExtractionProfile, error) {
	return s.repo.GetExtractionProfile(ctx, id)
}

func hasErrorIssues(issues []model.RecordIssue) bool {
	for _, issue := range issues {
		if issue.Severity == model.RecordIssueError {
			return true
		}
	}
	return false
}

func hasWarningIssues(issues []model.RecordIssue) bool {
	for _, issue := range issues {
		if issue.Severity == model.RecordIssueWarning {
			return true
		}
	}
	return false
}
