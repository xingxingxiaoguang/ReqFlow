package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type PublishService struct {
	repo     port.ReviewPipelineRepo
	datasets *DatasetService
}

func NewPublishService(repo port.ReviewPipelineRepo, datasets *DatasetService) (*PublishService, error) {
	if repo == nil {
		return nil, fmt.Errorf("publish pipeline: repo is required")
	}
	if datasets == nil {
		return nil, fmt.Errorf("publish pipeline: dataset service is required")
	}
	return &PublishService{repo: repo, datasets: datasets}, nil
}

type PublishApprovedRecordsInput struct {
	ApprovedRecordSetID string
	SourceTaskID        string
	SourceStepRunID     string
	ProducerAttempt     int
}

func (s *PublishService) PublishApprovedRecords(ctx context.Context,
	in PublishApprovedRecordsInput) (*model.DatasetBatch, error) {
	set, err := s.repo.GetApprovedRecordSet(ctx, strings.TrimSpace(in.ApprovedRecordSetID))
	if err != nil {
		return nil, fmt.Errorf("读取 ApprovedRecordSet: %w", err)
	}
	dataset, err := s.repo.GetAppendDataset(ctx, set.TargetDatasetID)
	if err != nil {
		return nil, fmt.Errorf("读取目标 Dataset: %w", err)
	}
	if dataset.Status != model.DatasetStatusActive || dataset.SchemaID != set.TargetSchemaID {
		return nil, fmt.Errorf("ApprovedRecordSet 的目标 Dataset 状态或 Schema 已变化")
	}
	schema, err := s.repo.GetDatasetSchema(ctx, dataset.SchemaID)
	if err != nil {
		return nil, fmt.Errorf("读取目标 DatasetSchema: %w", err)
	}
	decisions, err := s.repo.ListRecordReviewDecisions(ctx, set.ID)
	if err != nil {
		return nil, fmt.Errorf("读取审核决定: %w", err)
	}
	if len(decisions) != set.RecordCount {
		return nil, fmt.Errorf("ApprovedRecordSet 决策数量不完整")
	}
	items := make([]BatchItemInput, 0, set.ApprovedCount+set.EditedCount)
	for _, decision := range decisions {
		if decision.Action == model.ReviewActionExclude {
			continue
		}
		fields, err := logic.NormalizeDatasetItem(schema.JSONSchema, decision.Fields)
		if err != nil {
			return nil, fmt.Errorf("审核记录 %s 不符合 Schema: %w", decision.ID, err)
		}
		itemKey, fingerprint, err := logic.DatasetItemIdentity(schema.SchemaHash, dataset.KeyFields, fields)
		if err != nil {
			return nil, fmt.Errorf("审核记录 %s 主键非法: %w", decision.ID, err)
		}
		if itemKey != decision.ItemKey || fingerprint != decision.Fingerprint {
			return nil, fmt.Errorf("审核记录 %s 的不可变身份校验失败", decision.ID)
		}
		values, err := decodeFieldsMap(fields)
		if err != nil {
			return nil, err
		}
		provenance := decision.Provenance
		provenance.ValidationResultID = decision.ValidationResultID
		provenance.ApprovedRecordSetID = set.ID
		provenance.ReviewDecisionID = decision.ID
		provenance.ReviewAction = decision.Action
		items = append(items, BatchItemInput{Fields: values, Provenance: provenance})
	}
	if len(items) == 0 || len(items) != set.ApprovedCount+set.EditedCount {
		return nil, fmt.Errorf("ApprovedRecordSet 没有完整的可发布记录")
	}
	batch, err := s.datasets.GetOrCreateBatch(ctx, CreateBatchInput{DatasetID: set.TargetDatasetID,
		SourceTaskID: strings.TrimSpace(in.SourceTaskID), SourceStepRunID: strings.TrimSpace(in.SourceStepRunID)},
		in.ProducerAttempt)
	if err != nil {
		return nil, err
	}
	return s.datasets.CommitBatchForStep(ctx, batch.ID, in.SourceStepRunID, in.ProducerAttempt, items)
}

func decodeFieldsMap(raw json.RawMessage) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		if err == nil {
			err = fmt.Errorf("字段根节点必须是 object")
		}
		return nil, fmt.Errorf("解析审核字段: %w", err)
	}
	return fields, nil
}
