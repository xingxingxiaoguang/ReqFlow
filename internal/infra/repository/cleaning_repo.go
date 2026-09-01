package repository

import (
	"bytes"
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

func (r *PipelineRepo) BeginTransformedRecordSet(ctx context.Context, set *model.TransformedRecordSet) (*model.TransformedRecordSet, error) {
	if set.DraftCount < 0 || set.ProducerAttempt <= 0 || strings.TrimSpace(set.EngineVersion) == "" {
		return nil, fmt.Errorf("TransformedRecordSet 计划非法")
	}
	var stored model.TransformedRecordSet
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assertActiveNodeProducer(tx, set.ProducerNodeRunID, set.ProducerAttempt); err != nil {
			return err
		}
		var row transformedRecordSetRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("producer_node_run_id = ?", set.ProducerNodeRunID).
			Limit(1).Find(&row).Error; err != nil {
			return err
		}
		if row.ID == "" {
			if set.ID == "" {
				set.ID = uuid.NewString()
			}
			if set.CreatedAt.IsZero() {
				set.CreatedAt = time.Now()
			}
			if err := tx.Exec(`INSERT INTO transformed_record_sets
				(id, record_draft_set_id, data_contract_hash, extraction_spec_hash, schema_hash,
				 producer_node_run_id, status, producer_attempt, engine_version, draft_count, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, set.ID, set.RecordDraftSetID,
				set.DataContractHash, set.ExtractionSpecHash, set.SchemaHash, set.ProducerNodeRunID,
				model.TransformedRecordSetRunning, set.ProducerAttempt, set.EngineVersion,
				set.DraftCount, set.CreatedAt).Error; err != nil {
				return err
			}
			row = transformedRecordSetRow{ID: set.ID, RecordDraftSetID: set.RecordDraftSetID,
				DataContractHash: set.DataContractHash, ExtractionSpecHash: set.ExtractionSpecHash,
				SchemaHash:        set.SchemaHash,
				ProducerNodeRunID: set.ProducerNodeRunID, Status: model.TransformedRecordSetRunning,
				ProducerAttempt: set.ProducerAttempt, EngineVersion: set.EngineVersion,
				DraftCount: set.DraftCount, CreatedAt: set.CreatedAt}
		} else {
			if row.RecordDraftSetID != set.RecordDraftSetID || row.DataContractHash != set.DataContractHash ||
				row.ExtractionSpecHash != set.ExtractionSpecHash || row.SchemaHash != set.SchemaHash ||
				row.EngineVersion != set.EngineVersion ||
				row.DraftCount != set.DraftCount {
				return fmt.Errorf("NodeRun %s 的转换输入、内联合同或引擎版本发生变化", set.ProducerNodeRunID)
			}
			if row.ProducerAttempt > set.ProducerAttempt {
				return port.ErrStaleResourceExecution
			}
			if row.Status != model.TransformedRecordSetSucceeded && row.ProducerAttempt < set.ProducerAttempt {
				if err := tx.Model(&transformedRecordSetRow{}).Where("id = ?", row.ID).Updates(map[string]any{
					"producer_attempt": set.ProducerAttempt,
					"status":           model.TransformedRecordSetRunning,
					"finished_at":      nil,
				}).Error; err != nil {
					return err
				}
				row.ProducerAttempt, row.Status, row.FinishedAt = set.ProducerAttempt, model.TransformedRecordSetRunning, nil
			}
		}
		if err := refreshTransformedRecordSetCounts(tx, &row); err != nil {
			return err
		}
		stored = row.toModel()
		return nil
	})
	return &stored, err
}

func (r *PipelineRepo) GetTransformedRecordSet(ctx context.Context, id string) (*model.TransformedRecordSet, error) {
	return r.getTransformedRecordSet(ctx, "id = ?", id)
}

func (r *PipelineRepo) GetTransformedRecordSetByNodeRun(ctx context.Context, nodeRunID string) (*model.TransformedRecordSet, error) {
	return r.getTransformedRecordSet(ctx, "producer_node_run_id = ?", nodeRunID)
}

func (r *PipelineRepo) getTransformedRecordSet(ctx context.Context, query string, value any) (*model.TransformedRecordSet, error) {
	var row transformedRecordSetRow
	if err := r.db.WithContext(ctx).Where(query, value).First(&row).Error; err != nil {
		return nil, err
	}
	set := row.toModel()
	return &set, nil
}

func (r *PipelineRepo) SaveTransformedRecord(ctx context.Context, setID string, producerAttempt int, record *model.TransformedRecord) error {
	if record == nil {
		return fmt.Errorf("TransformedRecord 不能为空")
	}
	if err := requireJSONBObject("fields", record.Fields); err != nil {
		return fmt.Errorf("TransformedRecord: %w", err)
	}
	changes, err := marshalJSONBArray(record.Changes)
	if err != nil {
		return fmt.Errorf("序列化转换差异: %w", err)
	}
	issues, err := marshalJSONBArray(record.Issues)
	if err != nil {
		return fmt.Errorf("序列化转换问题: %w", err)
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		set, err := lockWritableTransformedRecordSet(tx, setID, producerAttempt)
		if err != nil {
			return err
		}
		if record.ID == "" {
			record.ID = uuid.NewString()
		}
		if record.CreatedAt.IsZero() {
			record.CreatedAt = time.Now()
		}
		record.TransformedRecordSetID = set.ID
		result := tx.Exec(`INSERT INTO transformed_records
			(id, transformed_record_set_id, record_draft_id, ordinal, fields, changes, issues, created_at)
			VALUES (?, ?, ?, ?, ?::jsonb, ?::jsonb, ?::jsonb, ?)
			ON CONFLICT (transformed_record_set_id, record_draft_id) DO NOTHING`,
			record.ID, set.ID, record.RecordDraftID, record.Ordinal, string(record.Fields),
			string(changes), string(issues), record.CreatedAt)
		if result.Error != nil {
			return result.Error
		}
		var stored transformedRecordRow
		if err := tx.Where("transformed_record_set_id = ? AND record_draft_id = ?", set.ID, record.RecordDraftID).
			First(&stored).Error; err != nil {
			return err
		}
		if stored.Ordinal != record.Ordinal || !equalJSON(stored.Fields, record.Fields) ||
			!equalJSON(stored.Changes, changes) || !equalJSON(stored.Issues, issues) {
			return fmt.Errorf("RecordDraft %s 的不可变转换结果发生变化", record.RecordDraftID)
		}
		record.ID, record.CreatedAt = stored.ID, stored.CreatedAt
		return refreshTransformedRecordSetCounts(tx, set)
	})
}

func (r *PipelineRepo) FinalizeTransformedRecordSet(ctx context.Context, setID string, producerAttempt int) (*model.TransformedRecordSet, error) {
	var completed model.TransformedRecordSet
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := lockWritableTransformedRecordSet(tx, setID, producerAttempt)
		if err != nil {
			return err
		}
		if err := refreshTransformedRecordSetCounts(tx, row); err != nil {
			return err
		}
		if row.TransformedCount != row.DraftCount {
			return fmt.Errorf("TransformedRecordSet 尚有 %d 条草稿未转换", row.DraftCount-row.TransformedCount)
		}
		now := time.Now()
		if err := tx.Model(&transformedRecordSetRow{}).Where("id = ?", row.ID).Updates(map[string]any{
			"status": model.TransformedRecordSetSucceeded, "finished_at": now,
		}).Error; err != nil {
			return err
		}
		row.Status, row.FinishedAt = model.TransformedRecordSetSucceeded, &now
		completed = row.toModel()
		return nil
	})
	return &completed, err
}

func (r *PipelineRepo) ListTransformedRecords(ctx context.Context, setID string) ([]model.TransformedRecord, error) {
	var rows []transformedRecordRow
	if err := r.db.WithContext(ctx).Where("transformed_record_set_id = ?", setID).
		Order("ordinal ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.TransformedRecord, len(rows))
	for i := range rows {
		item, err := rows[i].toModel()
		if err != nil {
			return nil, err
		}
		out[i] = item
	}
	return out, nil
}

func (r *PipelineRepo) BeginValidationResultSet(ctx context.Context, set *model.ValidationResultSet) (*model.ValidationResultSet, error) {
	if set.RecordCount < 0 || set.ValidatedThroughSeq < 0 || set.ProducerAttempt <= 0 || strings.TrimSpace(set.EngineVersion) == "" {
		return nil, fmt.Errorf("ValidationResultSet 计划非法")
	}
	var stored model.ValidationResultSet
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assertActiveNodeProducer(tx, set.ProducerNodeRunID, set.ProducerAttempt); err != nil {
			return err
		}
		var row validationResultSetRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("producer_node_run_id = ?", set.ProducerNodeRunID).
			Limit(1).Find(&row).Error; err != nil {
			return err
		}
		if row.ID == "" {
			if set.ID == "" {
				set.ID = uuid.NewString()
			}
			if set.CreatedAt.IsZero() {
				set.CreatedAt = time.Now()
			}
			if err := tx.Exec(`INSERT INTO validation_result_sets
				(id, transformed_record_set_id, target_dataset_id, target_schema_id,
				 producer_node_run_id, status, producer_attempt, engine_version,
				 validated_through_seq, record_count, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, set.ID, set.TransformedRecordSetID,
				set.TargetDatasetID, set.TargetSchemaID, set.ProducerNodeRunID,
				model.ValidationResultSetRunning, set.ProducerAttempt, set.EngineVersion,
				set.ValidatedThroughSeq, set.RecordCount, set.CreatedAt).Error; err != nil {
				return err
			}
			row = validationResultSetRow{ID: set.ID, TransformedRecordSetID: set.TransformedRecordSetID,
				TargetDatasetID: set.TargetDatasetID, TargetSchemaID: set.TargetSchemaID,
				ProducerNodeRunID: set.ProducerNodeRunID, Status: model.ValidationResultSetRunning,
				ProducerAttempt: set.ProducerAttempt, EngineVersion: set.EngineVersion,
				ValidatedThroughSeq: set.ValidatedThroughSeq, RecordCount: set.RecordCount,
				CreatedAt: set.CreatedAt}
		} else {
			if row.TransformedRecordSetID != set.TransformedRecordSetID || row.TargetDatasetID != set.TargetDatasetID ||
				row.TargetSchemaID != set.TargetSchemaID || row.EngineVersion != set.EngineVersion ||
				row.RecordCount != set.RecordCount {
				return fmt.Errorf("NodeRun %s 的校验输入、Dataset 边界、Schema 或引擎版本发生变化", set.ProducerNodeRunID)
			}
			if row.ProducerAttempt > set.ProducerAttempt {
				return port.ErrStaleResourceExecution
			}
			if row.Status != model.ValidationResultSetSucceeded && row.ProducerAttempt < set.ProducerAttempt {
				if err := tx.Model(&validationResultSetRow{}).Where("id = ?", row.ID).Updates(map[string]any{
					"producer_attempt": set.ProducerAttempt,
					"status":           model.ValidationResultSetRunning,
					"finished_at":      nil,
				}).Error; err != nil {
					return err
				}
				row.ProducerAttempt, row.Status, row.FinishedAt = set.ProducerAttempt, model.ValidationResultSetRunning, nil
			}
		}
		if err := refreshValidationResultSetCounts(tx, &row); err != nil {
			return err
		}
		stored = row.toModel()
		return nil
	})
	return &stored, err
}

func (r *PipelineRepo) GetValidationResultSet(ctx context.Context, id string) (*model.ValidationResultSet, error) {
	return r.getValidationResultSet(ctx, "id = ?", id)
}

func (r *PipelineRepo) GetValidationResultSetByNodeRun(ctx context.Context, nodeRunID string) (*model.ValidationResultSet, error) {
	return r.getValidationResultSet(ctx, "producer_node_run_id = ?", nodeRunID)
}

func (r *PipelineRepo) getValidationResultSet(ctx context.Context, query string, value any) (*model.ValidationResultSet, error) {
	var row validationResultSetRow
	if err := r.db.WithContext(ctx).Where(query, value).First(&row).Error; err != nil {
		return nil, err
	}
	set := row.toModel()
	return &set, nil
}

func (r *PipelineRepo) SaveValidationResult(ctx context.Context, setID string, producerAttempt int, result *model.ValidationResult) error {
	if result == nil {
		return fmt.Errorf("ValidationResult 不能为空")
	}
	if err := requireJSONBObject("fields", result.Fields); err != nil {
		return fmt.Errorf("ValidationResult: %w", err)
	}
	if !validValidationRecordStatus(result.Status) {
		return fmt.Errorf("ValidationResult status 非法: %s", result.Status)
	}
	issues, err := marshalJSONBArray(result.Issues)
	if err != nil {
		return fmt.Errorf("序列化校验问题: %w", err)
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		set, err := lockWritableValidationResultSet(tx, setID, producerAttempt)
		if err != nil {
			return err
		}
		if result.ID == "" {
			result.ID = uuid.NewString()
		}
		if result.CreatedAt.IsZero() {
			result.CreatedAt = time.Now()
		}
		result.ValidationResultSetID = set.ID
		insert := tx.Exec(`INSERT INTO validation_results
			(id, validation_result_set_id, transformed_record_id, ordinal, fields,
			 item_key, fingerprint, status, issues, created_at)
			VALUES (?, ?, ?, ?, ?::jsonb, ?, ?, ?, ?::jsonb, ?)
			ON CONFLICT (validation_result_set_id, transformed_record_id) DO NOTHING`,
			result.ID, set.ID, result.TransformedRecordID, result.Ordinal, string(result.Fields),
			result.ItemKey, result.Fingerprint, result.Status, string(issues), result.CreatedAt)
		if insert.Error != nil {
			return insert.Error
		}
		var stored validationResultRow
		if err := tx.Where("validation_result_set_id = ? AND transformed_record_id = ?", set.ID, result.TransformedRecordID).
			First(&stored).Error; err != nil {
			return err
		}
		if stored.Ordinal != result.Ordinal || stored.ItemKey != result.ItemKey || stored.Fingerprint != result.Fingerprint ||
			stored.Status != result.Status || !equalJSON(stored.Fields, result.Fields) || !equalJSON(stored.Issues, issues) {
			return fmt.Errorf("TransformedRecord %s 的不可变校验结果发生变化", result.TransformedRecordID)
		}
		result.ID, result.CreatedAt = stored.ID, stored.CreatedAt
		return refreshValidationResultSetCounts(tx, set)
	})
}

func (r *PipelineRepo) FinalizeValidationResultSet(ctx context.Context, setID string, producerAttempt int) (*model.ValidationResultSet, error) {
	var completed model.ValidationResultSet
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := lockWritableValidationResultSet(tx, setID, producerAttempt)
		if err != nil {
			return err
		}
		if err := refreshValidationResultSetCounts(tx, row); err != nil {
			return err
		}
		completedCount := row.ValidCount + row.WarningCount + row.InvalidCount + row.DuplicateCount + row.ConflictCount
		if completedCount != row.RecordCount {
			return fmt.Errorf("ValidationResultSet 尚有 %d 条记录未校验", row.RecordCount-completedCount)
		}
		now := time.Now()
		if err := tx.Model(&validationResultSetRow{}).Where("id = ?", row.ID).Updates(map[string]any{
			"status": model.ValidationResultSetSucceeded, "finished_at": now,
		}).Error; err != nil {
			return err
		}
		row.Status, row.FinishedAt = model.ValidationResultSetSucceeded, &now
		completed = row.toModel()
		return nil
	})
	return &completed, err
}

func (r *PipelineRepo) ListValidationResults(ctx context.Context, setID string) ([]model.ValidationResult, error) {
	var rows []validationResultRow
	if err := r.db.WithContext(ctx).Where("validation_result_set_id = ?", setID).
		Order("ordinal ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.ValidationResult, len(rows))
	for i := range rows {
		item, err := rows[i].toModel()
		if err != nil {
			return nil, err
		}
		out[i] = item
	}
	return out, nil
}

func (r *PipelineRepo) FindExistingDatasetItemKeys(ctx context.Context, datasetID string, throughSeq int64, itemKeys []string) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	unique := make(map[string]bool, len(itemKeys))
	keys := make([]string, 0, len(itemKeys))
	for _, key := range itemKeys {
		if key = strings.TrimSpace(key); key != "" && !unique[key] {
			unique[key] = true
			keys = append(keys, key)
		}
	}
	const chunkSize = 500
	for start := 0; start < len(keys); start += chunkSize {
		end := start + chunkSize
		if end > len(keys) {
			end = len(keys)
		}
		var found []string
		if err := r.db.WithContext(ctx).Table("dataset_items").Distinct("item_key").
			Where("dataset_id = ? AND commit_seq <= ? AND item_key IN ?", datasetID, throughSeq, keys[start:end]).
			Pluck("item_key", &found).Error; err != nil {
			return nil, err
		}
		for _, key := range found {
			out[key] = struct{}{}
		}
	}
	return out, nil
}

func lockWritableTransformedRecordSet(tx *gorm.DB, setID string, attempt int) (*transformedRecordSetRow, error) {
	var identity struct{ ProducerNodeRunID string }
	if err := tx.Model(&transformedRecordSetRow{}).Select("producer_node_run_id").Where("id = ?", setID).Take(&identity).Error; err != nil {
		return nil, err
	}
	if err := assertActiveNodeProducer(tx, identity.ProducerNodeRunID, attempt); err != nil {
		return nil, err
	}
	var row transformedRecordSetRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", setID).First(&row).Error; err != nil {
		return nil, err
	}
	if row.ProducerNodeRunID != identity.ProducerNodeRunID || row.ProducerAttempt != attempt || row.Status != model.TransformedRecordSetRunning {
		return nil, port.ErrStaleResourceExecution
	}
	return &row, nil
}

func lockWritableValidationResultSet(tx *gorm.DB, setID string, attempt int) (*validationResultSetRow, error) {
	var identity struct{ ProducerNodeRunID string }
	if err := tx.Model(&validationResultSetRow{}).Select("producer_node_run_id").Where("id = ?", setID).Take(&identity).Error; err != nil {
		return nil, err
	}
	if err := assertActiveNodeProducer(tx, identity.ProducerNodeRunID, attempt); err != nil {
		return nil, err
	}
	var row validationResultSetRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", setID).First(&row).Error; err != nil {
		return nil, err
	}
	if row.ProducerNodeRunID != identity.ProducerNodeRunID || row.ProducerAttempt != attempt || row.Status != model.ValidationResultSetRunning {
		return nil, port.ErrStaleResourceExecution
	}
	return &row, nil
}

func refreshTransformedRecordSetCounts(tx *gorm.DB, row *transformedRecordSetRow) error {
	var counts struct {
		Total   int
		Changed int
		Issues  int
	}
	if err := tx.Raw(`SELECT count(*) AS total,
		count(*) FILTER (WHERE jsonb_array_length(changes) > 0) AS changed,
		COALESCE(sum(jsonb_array_length(issues)), 0) AS issues
		FROM transformed_records WHERE transformed_record_set_id = ?`, row.ID).Scan(&counts).Error; err != nil {
		return err
	}
	if err := tx.Model(&transformedRecordSetRow{}).Where("id = ?", row.ID).Updates(map[string]any{
		"transformed_count": counts.Total, "changed_record_count": counts.Changed, "issue_count": counts.Issues,
	}).Error; err != nil {
		return err
	}
	row.TransformedCount, row.ChangedRecordCount, row.IssueCount = counts.Total, counts.Changed, counts.Issues
	return nil
}

func refreshValidationResultSetCounts(tx *gorm.DB, row *validationResultSetRow) error {
	var counts struct {
		Valid     int
		Warning   int
		Invalid   int
		Duplicate int
		Conflict  int
	}
	if err := tx.Raw(`SELECT
		count(*) FILTER (WHERE status = ?) AS valid,
		count(*) FILTER (WHERE status = ?) AS warning,
		count(*) FILTER (WHERE status = ?) AS invalid,
		count(*) FILTER (WHERE status = ?) AS duplicate,
		count(*) FILTER (WHERE status = ?) AS conflict
		FROM validation_results WHERE validation_result_set_id = ?`,
		model.ValidationRecordValid, model.ValidationRecordWarning, model.ValidationRecordInvalid,
		model.ValidationRecordDuplicate, model.ValidationRecordConflict, row.ID).Scan(&counts).Error; err != nil {
		return err
	}
	if err := tx.Model(&validationResultSetRow{}).Where("id = ?", row.ID).Updates(map[string]any{
		"valid_count": counts.Valid, "warning_count": counts.Warning, "invalid_count": counts.Invalid,
		"duplicate_count": counts.Duplicate, "conflict_count": counts.Conflict,
	}).Error; err != nil {
		return err
	}
	row.ValidCount, row.WarningCount, row.InvalidCount = counts.Valid, counts.Warning, counts.Invalid
	row.DuplicateCount, row.ConflictCount = counts.Duplicate, counts.Conflict
	return nil
}

func validValidationRecordStatus(status string) bool {
	switch status {
	case model.ValidationRecordValid, model.ValidationRecordWarning, model.ValidationRecordInvalid,
		model.ValidationRecordDuplicate, model.ValidationRecordConflict:
		return true
	default:
		return false
	}
}

func equalJSON(left, right []byte) bool {
	var a, b any
	if decodeSingleJSONForRepo(left, &a) != nil || decodeSingleJSONForRepo(right, &b) != nil {
		return bytes.Equal(left, right)
	}
	aRaw, _ := json.Marshal(a)
	bRaw, _ := json.Marshal(b)
	return bytes.Equal(aRaw, bRaw)
}

func decodeSingleJSONForRepo(raw []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(dst)
}

type transformedRecordSetRow struct {
	ID                 string     `gorm:"column:id;primaryKey"`
	RecordDraftSetID   string     `gorm:"column:record_draft_set_id"`
	DataContractHash   string     `gorm:"column:data_contract_hash"`
	ExtractionSpecHash string     `gorm:"column:extraction_spec_hash"`
	SchemaHash         string     `gorm:"column:schema_hash"`
	ProducerNodeRunID  string     `gorm:"column:producer_node_run_id"`
	Status             string     `gorm:"column:status"`
	ProducerAttempt    int        `gorm:"column:producer_attempt"`
	EngineVersion      string     `gorm:"column:engine_version"`
	DraftCount         int        `gorm:"column:draft_count"`
	TransformedCount   int        `gorm:"column:transformed_count"`
	ChangedRecordCount int        `gorm:"column:changed_record_count"`
	IssueCount         int        `gorm:"column:issue_count"`
	CreatedAt          time.Time  `gorm:"column:created_at"`
	FinishedAt         *time.Time `gorm:"column:finished_at"`
}

func (transformedRecordSetRow) TableName() string { return "transformed_record_sets" }
func (row transformedRecordSetRow) toModel() model.TransformedRecordSet {
	set := model.TransformedRecordSet{ID: row.ID, RecordDraftSetID: row.RecordDraftSetID,
		DataContractHash: row.DataContractHash, ExtractionSpecHash: row.ExtractionSpecHash,
		SchemaHash:        row.SchemaHash,
		ProducerNodeRunID: row.ProducerNodeRunID, Status: row.Status, ProducerAttempt: row.ProducerAttempt,
		EngineVersion: row.EngineVersion, DraftCount: row.DraftCount, TransformedCount: row.TransformedCount,
		ChangedRecordCount: row.ChangedRecordCount, IssueCount: row.IssueCount, CreatedAt: row.CreatedAt}
	if row.FinishedAt != nil {
		set.FinishedAt = *row.FinishedAt
	}
	return set
}

type transformedRecordRow struct {
	ID                     string          `gorm:"column:id;primaryKey"`
	TransformedRecordSetID string          `gorm:"column:transformed_record_set_id"`
	RecordDraftID          string          `gorm:"column:record_draft_id"`
	Ordinal                int             `gorm:"column:ordinal"`
	Fields                 json.RawMessage `gorm:"column:fields;type:jsonb"`
	Changes                json.RawMessage `gorm:"column:changes;type:jsonb"`
	Issues                 json.RawMessage `gorm:"column:issues;type:jsonb"`
	CreatedAt              time.Time       `gorm:"column:created_at"`
}

func (transformedRecordRow) TableName() string { return "transformed_records" }
func (row transformedRecordRow) toModel() (model.TransformedRecord, error) {
	var changes []model.RecordChange
	var issues []model.RecordIssue
	if err := json.Unmarshal(row.Changes, &changes); err != nil {
		return model.TransformedRecord{}, fmt.Errorf("解析 TransformedRecord %s changes: %w", row.ID, err)
	}
	if err := json.Unmarshal(row.Issues, &issues); err != nil {
		return model.TransformedRecord{}, fmt.Errorf("解析 TransformedRecord %s issues: %w", row.ID, err)
	}
	return model.TransformedRecord{ID: row.ID, TransformedRecordSetID: row.TransformedRecordSetID,
		RecordDraftID: row.RecordDraftID, Ordinal: row.Ordinal, Fields: row.Fields,
		Changes: changes, Issues: issues, CreatedAt: row.CreatedAt}, nil
}

type validationResultSetRow struct {
	ID                     string     `gorm:"column:id;primaryKey"`
	TransformedRecordSetID string     `gorm:"column:transformed_record_set_id"`
	TargetDatasetID        string     `gorm:"column:target_dataset_id"`
	TargetSchemaID         string     `gorm:"column:target_schema_id"`
	ProducerNodeRunID      string     `gorm:"column:producer_node_run_id"`
	Status                 string     `gorm:"column:status"`
	ProducerAttempt        int        `gorm:"column:producer_attempt"`
	EngineVersion          string     `gorm:"column:engine_version"`
	ValidatedThroughSeq    int64      `gorm:"column:validated_through_seq"`
	RecordCount            int        `gorm:"column:record_count"`
	ValidCount             int        `gorm:"column:valid_count"`
	WarningCount           int        `gorm:"column:warning_count"`
	InvalidCount           int        `gorm:"column:invalid_count"`
	DuplicateCount         int        `gorm:"column:duplicate_count"`
	ConflictCount          int        `gorm:"column:conflict_count"`
	CreatedAt              time.Time  `gorm:"column:created_at"`
	FinishedAt             *time.Time `gorm:"column:finished_at"`
}

func (validationResultSetRow) TableName() string { return "validation_result_sets" }
func (row validationResultSetRow) toModel() model.ValidationResultSet {
	set := model.ValidationResultSet{ID: row.ID, TransformedRecordSetID: row.TransformedRecordSetID,
		TargetDatasetID: row.TargetDatasetID, TargetSchemaID: row.TargetSchemaID,
		ProducerNodeRunID: row.ProducerNodeRunID, Status: row.Status, ProducerAttempt: row.ProducerAttempt,
		EngineVersion: row.EngineVersion, ValidatedThroughSeq: row.ValidatedThroughSeq,
		RecordCount: row.RecordCount, ValidCount: row.ValidCount, WarningCount: row.WarningCount,
		InvalidCount: row.InvalidCount, DuplicateCount: row.DuplicateCount,
		ConflictCount: row.ConflictCount, CreatedAt: row.CreatedAt}
	if row.FinishedAt != nil {
		set.FinishedAt = *row.FinishedAt
	}
	return set
}

type validationResultRow struct {
	ID                    string          `gorm:"column:id;primaryKey"`
	ValidationResultSetID string          `gorm:"column:validation_result_set_id"`
	TransformedRecordID   string          `gorm:"column:transformed_record_id"`
	Ordinal               int             `gorm:"column:ordinal"`
	Fields                json.RawMessage `gorm:"column:fields;type:jsonb"`
	ItemKey               string          `gorm:"column:item_key"`
	Fingerprint           string          `gorm:"column:fingerprint"`
	Status                string          `gorm:"column:status"`
	Issues                json.RawMessage `gorm:"column:issues;type:jsonb"`
	CreatedAt             time.Time       `gorm:"column:created_at"`
}

func (validationResultRow) TableName() string { return "validation_results" }
func (row validationResultRow) toModel() (model.ValidationResult, error) {
	var issues []model.RecordIssue
	if err := json.Unmarshal(row.Issues, &issues); err != nil {
		return model.ValidationResult{}, fmt.Errorf("解析 ValidationResult %s issues: %w", row.ID, err)
	}
	return model.ValidationResult{ID: row.ID, ValidationResultSetID: row.ValidationResultSetID,
		TransformedRecordID: row.TransformedRecordID, Ordinal: row.Ordinal, Fields: row.Fields,
		ItemKey: row.ItemKey, Fingerprint: row.Fingerprint, Status: row.Status,
		Issues: issues, CreatedAt: row.CreatedAt}, nil
}
