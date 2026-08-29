package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

func (r *PipelineRepo) CreateApprovedRecordSet(ctx context.Context, set *model.ApprovedRecordSet,
	decisions []model.RecordReviewDecision) (*model.ApprovedRecordSet, error) {
	if set == nil || strings.TrimSpace(set.SourceStepRunID) == "" || strings.TrimSpace(set.ValidationResultSetID) == "" ||
		strings.TrimSpace(set.Reviewer) == "" || strings.TrimSpace(set.Rationale) == "" || strings.TrimSpace(set.ReviewHash) == "" {
		return nil, fmt.Errorf("ApprovedRecordSet 审核身份不完整")
	}
	if set.RecordCount <= 0 || len(decisions) != set.RecordCount ||
		set.ApprovedCount+set.EditedCount+set.ExcludedCount != set.RecordCount ||
		set.ApprovedCount+set.EditedCount == 0 {
		return nil, fmt.Errorf("ApprovedRecordSet 决策数量非法")
	}

	var stored model.ApprovedRecordSet
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing approvedRecordSetRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("source_step_run_id = ?", set.SourceStepRunID).Limit(1).Find(&existing).Error; err != nil {
			return err
		}
		if existing.ID != "" {
			if existing.ReviewHash != set.ReviewHash {
				return fmt.Errorf("人工 Gate %s 已存在不同内容的不可变审核结论", set.SourceStepRunID)
			}
			stored = existing.toModel()
			return nil
		}

		var step struct {
			Kind   model.StepKind
			Status string
		}
		if err := tx.Raw(`SELECT kind, status FROM step_runs WHERE id = ? FOR UPDATE`,
			set.SourceStepRunID).Scan(&step).Error; err != nil {
			return err
		}
		if step.Kind != model.StepKindHumanReview || step.Status != model.StepRunAwaiting {
			return fmt.Errorf("%w: 人工 Gate 不再处于 awaiting", port.ErrInvalidTransition)
		}
		var validation struct {
			TargetDatasetID string
			TargetSchemaID  string
			Status          string
			RecordCount     int
		}
		if err := tx.Raw(`SELECT target_dataset_id, target_schema_id, status, record_count
			FROM validation_result_sets WHERE id = ?`, set.ValidationResultSetID).Scan(&validation).Error; err != nil {
			return err
		}
		if validation.TargetDatasetID == "" || validation.Status != model.ValidationResultSetSucceeded ||
			validation.TargetDatasetID != set.TargetDatasetID || validation.TargetSchemaID != set.TargetSchemaID ||
			validation.RecordCount != set.RecordCount {
			return fmt.Errorf("ApprovedRecordSet 与 ValidationResultSet 边界不一致")
		}

		if set.ID == "" {
			set.ID = uuid.NewString()
		}
		if set.CreatedAt.IsZero() {
			set.CreatedAt = time.Now()
		}
		if err := tx.Exec(`INSERT INTO approved_record_sets
			(id, validation_result_set_id, target_dataset_id, target_schema_id, source_step_run_id,
			 reviewer, rationale, review_hash, reviewed_through_seq, record_count,
			 approved_count, edited_count, excluded_count, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, set.ID,
			set.ValidationResultSetID, set.TargetDatasetID, set.TargetSchemaID, set.SourceStepRunID,
			set.Reviewer, set.Rationale, set.ReviewHash, set.ReviewedThroughSeq, set.RecordCount,
			set.ApprovedCount, set.EditedCount, set.ExcludedCount, set.CreatedAt).Error; err != nil {
			return err
		}

		seen := make(map[string]bool, len(decisions))
		for i := range decisions {
			decision := &decisions[i]
			if seen[decision.ValidationResultID] || !validReviewAction(decision.Action) ||
				!json.Valid(decision.Fields) {
				return fmt.Errorf("第 %d 条审核决定非法", i+1)
			}
			seen[decision.ValidationResultID] = true
			issues, err := json.Marshal(decision.Issues)
			if err != nil {
				return fmt.Errorf("序列化第 %d 条审核问题: %w", i+1, err)
			}
			provenance, err := json.Marshal(decision.Provenance)
			if err != nil {
				return fmt.Errorf("序列化第 %d 条 provenance: %w", i+1, err)
			}
			if decision.ID == "" {
				decision.ID = uuid.NewString()
			}
			decision.ApprovedRecordSetID = set.ID
			decision.CreatedAt = set.CreatedAt
			result := tx.Exec(`INSERT INTO record_review_decisions
				(id, approved_record_set_id, validation_result_id, transformed_record_id,
				 ordinal, action, fields, item_key, fingerprint, issues, provenance, note, created_at)
				SELECT ?, ?, v.id, v.transformed_record_id, ?, ?, ?::jsonb, ?, ?, ?::jsonb, ?::jsonb, ?, ?
				FROM validation_results AS v
				WHERE v.id = ? AND v.validation_result_set_id = ?`, decision.ID, set.ID,
				decision.Ordinal, decision.Action, string(decision.Fields), decision.ItemKey,
				decision.Fingerprint, string(issues), string(provenance), decision.Note,
				decision.CreatedAt, decision.ValidationResultID, set.ValidationResultSetID)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("ValidationResult %s 不属于当前审核集合", decision.ValidationResultID)
			}
		}
		stored = *set
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &stored, nil
}

func (r *PipelineRepo) GetApprovedRecordSet(ctx context.Context, id string) (*model.ApprovedRecordSet, error) {
	return r.getApprovedRecordSet(ctx, "id = ?", id)
}

func (r *PipelineRepo) FindApprovedRecordSetByStepRun(ctx context.Context, stepRunID string) (*model.ApprovedRecordSet, bool, error) {
	var row approvedRecordSetRow
	if err := r.db.WithContext(ctx).Where("source_step_run_id = ?", stepRunID).Limit(1).Find(&row).Error; err != nil {
		return nil, false, err
	}
	if row.ID == "" {
		return nil, false, nil
	}
	set := row.toModel()
	return &set, true, nil
}

func (r *PipelineRepo) getApprovedRecordSet(ctx context.Context, query string, value any) (*model.ApprovedRecordSet, error) {
	var row approvedRecordSetRow
	if err := r.db.WithContext(ctx).Where(query, value).First(&row).Error; err != nil {
		return nil, err
	}
	set := row.toModel()
	return &set, nil
}

func (r *PipelineRepo) ListRecordReviewDecisions(ctx context.Context, setID string) ([]model.RecordReviewDecision, error) {
	var rows []recordReviewDecisionRow
	if err := r.db.WithContext(ctx).Where("approved_record_set_id = ?", setID).
		Order("ordinal ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.RecordReviewDecision, len(rows))
	for i := range rows {
		decision, err := rows[i].toModel()
		if err != nil {
			return nil, err
		}
		out[i] = decision
	}
	return out, nil
}

func validReviewAction(action string) bool {
	return action == model.ReviewActionApprove || action == model.ReviewActionEdit || action == model.ReviewActionExclude
}

type approvedRecordSetRow struct {
	ID                    string    `gorm:"column:id;primaryKey"`
	ValidationResultSetID string    `gorm:"column:validation_result_set_id"`
	TargetDatasetID       string    `gorm:"column:target_dataset_id"`
	TargetSchemaID        string    `gorm:"column:target_schema_id"`
	SourceStepRunID       string    `gorm:"column:source_step_run_id"`
	Reviewer              string    `gorm:"column:reviewer"`
	Rationale             string    `gorm:"column:rationale"`
	ReviewHash            string    `gorm:"column:review_hash"`
	ReviewedThroughSeq    int64     `gorm:"column:reviewed_through_seq"`
	RecordCount           int       `gorm:"column:record_count"`
	ApprovedCount         int       `gorm:"column:approved_count"`
	EditedCount           int       `gorm:"column:edited_count"`
	ExcludedCount         int       `gorm:"column:excluded_count"`
	CreatedAt             time.Time `gorm:"column:created_at"`
}

func (approvedRecordSetRow) TableName() string { return "approved_record_sets" }

func (row approvedRecordSetRow) toModel() model.ApprovedRecordSet {
	return model.ApprovedRecordSet{ID: row.ID, ValidationResultSetID: row.ValidationResultSetID,
		TargetDatasetID: row.TargetDatasetID, TargetSchemaID: row.TargetSchemaID,
		SourceStepRunID: row.SourceStepRunID, Reviewer: row.Reviewer, Rationale: row.Rationale,
		ReviewHash: row.ReviewHash, ReviewedThroughSeq: row.ReviewedThroughSeq,
		RecordCount: row.RecordCount, ApprovedCount: row.ApprovedCount,
		EditedCount: row.EditedCount, ExcludedCount: row.ExcludedCount, CreatedAt: row.CreatedAt}
}

type recordReviewDecisionRow struct {
	ID                  string    `gorm:"column:id;primaryKey"`
	ApprovedRecordSetID string    `gorm:"column:approved_record_set_id"`
	ValidationResultID  string    `gorm:"column:validation_result_id"`
	TransformedRecordID string    `gorm:"column:transformed_record_id"`
	Ordinal             int       `gorm:"column:ordinal"`
	Action              string    `gorm:"column:action"`
	Fields              string    `gorm:"column:fields;type:jsonb"`
	ItemKey             string    `gorm:"column:item_key"`
	Fingerprint         string    `gorm:"column:fingerprint"`
	Issues              string    `gorm:"column:issues;type:jsonb"`
	Provenance          string    `gorm:"column:provenance;type:jsonb"`
	Note                string    `gorm:"column:note"`
	CreatedAt           time.Time `gorm:"column:created_at"`
}

func (recordReviewDecisionRow) TableName() string { return "record_review_decisions" }

func (row recordReviewDecisionRow) toModel() (model.RecordReviewDecision, error) {
	var issues []model.RecordIssue
	if err := json.Unmarshal([]byte(row.Issues), &issues); err != nil {
		return model.RecordReviewDecision{}, fmt.Errorf("解析审核问题: %w", err)
	}
	var provenance model.ItemProvenance
	if err := json.Unmarshal([]byte(row.Provenance), &provenance); err != nil {
		return model.RecordReviewDecision{}, fmt.Errorf("解析审核 provenance: %w", err)
	}
	return model.RecordReviewDecision{ID: row.ID, ApprovedRecordSetID: row.ApprovedRecordSetID,
		ValidationResultID: row.ValidationResultID, TransformedRecordID: row.TransformedRecordID,
		Ordinal: row.Ordinal, Action: row.Action, Fields: json.RawMessage(row.Fields),
		ItemKey: row.ItemKey, Fingerprint: row.Fingerprint, Issues: issues,
		Provenance: provenance, Note: row.Note, CreatedAt: row.CreatedAt}, nil
}
