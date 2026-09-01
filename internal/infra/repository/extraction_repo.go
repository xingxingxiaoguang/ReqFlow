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

func (r *PipelineRepo) BeginRecordDraftSet(ctx context.Context, set *model.RecordDraftSet, units []model.ExtractionUnit) (*model.RecordDraftSet, error) {
	if set == nil || len(set.DataContract) == 0 || len(set.ExtractionSpec) == 0 || len(set.JSONSchema) == 0 ||
		strings.TrimSpace(set.DataContractHash) == "" || strings.TrimSpace(set.ExtractionSpecHash) == "" ||
		strings.TrimSpace(set.SchemaHash) == "" {
		return nil, fmt.Errorf("RecordDraftSet 必须固化完整内联合同")
	}
	var stored model.RecordDraftSet
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assertActiveNodeProducer(tx, set.ProducerNodeRunID, set.ProducerAttempt); err != nil {
			return err
		}

		var row recordDraftSetRow
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("producer_node_run_id = ?", set.ProducerNodeRunID).Limit(1).Find(&row).Error
		switch {
		case err != nil:
			return err
		case row.ID == "":
			if set.ID == "" {
				set.ID = uuid.NewString()
			}
			if set.CreatedAt.IsZero() {
				set.CreatedAt = time.Now()
			}
			if err := tx.Exec(`INSERT INTO record_draft_sets
				(id, parsed_document_set_id, data_contract, data_contract_hash, extraction_spec,
				 extraction_spec_hash, json_schema, schema_hash, producer_node_run_id, status,
				 producer_attempt, model, unit_count, created_at)
				VALUES (?, ?, ?::jsonb, ?, ?::jsonb, ?, ?::jsonb, ?, ?, ?, ?, ?, ?, ?)`,
				set.ID, set.ParsedDocumentSetID, string(set.DataContract), set.DataContractHash,
				string(set.ExtractionSpec), set.ExtractionSpecHash, string(set.JSONSchema), set.SchemaHash,
				set.ProducerNodeRunID, model.RecordDraftSetRunning, set.ProducerAttempt, set.Model,
				len(units), set.CreatedAt).Error; err != nil {
				return err
			}
			row = recordDraftSetRow{ID: set.ID, ParsedDocumentSetID: set.ParsedDocumentSetID,
				DataContract: set.DataContract, DataContractHash: set.DataContractHash,
				ExtractionSpec: set.ExtractionSpec, ExtractionSpecHash: set.ExtractionSpecHash,
				JSONSchema: set.JSONSchema, SchemaHash: set.SchemaHash, ProducerNodeRunID: set.ProducerNodeRunID,
				Status: model.RecordDraftSetRunning, ProducerAttempt: set.ProducerAttempt,
				Model: set.Model, UnitCount: len(units), CreatedAt: set.CreatedAt}
		default:
			if row.ProducerAttempt > set.ProducerAttempt {
				return port.ErrStaleResourceExecution
			}
			if row.ParsedDocumentSetID != set.ParsedDocumentSetID || row.DataContractHash != set.DataContractHash ||
				row.ExtractionSpecHash != set.ExtractionSpecHash || row.SchemaHash != set.SchemaHash || row.Model != set.Model ||
				!equalJSON(row.DataContract, set.DataContract) || !equalJSON(row.ExtractionSpec, set.ExtractionSpec) ||
				!equalJSON(row.JSONSchema, set.JSONSchema) {
				return fmt.Errorf("NodeRun %s 的抽取输入、内联合同或模型发生变化", set.ProducerNodeRunID)
			}
			if row.UnitCount != len(units) {
				return fmt.Errorf("NodeRun %s 的抽取分块计划发生变化", set.ProducerNodeRunID)
			}
			if row.ProducerAttempt < set.ProducerAttempt {
				if err := tx.Model(&recordDraftSetRow{}).Where("id = ?", row.ID).Updates(map[string]any{
					"producer_attempt":  set.ProducerAttempt,
					"status":            model.RecordDraftSetRunning,
					"failed_unit_count": 0,
					"finished_at":       nil,
				}).Error; err != nil {
					return err
				}
				if err := tx.Exec(`DELETE FROM record_drafts WHERE extraction_unit_id IN
					(SELECT id FROM extraction_units WHERE record_draft_set_id = ? AND status <> ?)`,
					row.ID, model.ExtractionUnitSucceeded).Error; err != nil {
					return err
				}
				if err := tx.Model(&extractionUnitRow{}).
					Where("record_draft_set_id = ? AND status <> ?", row.ID, model.ExtractionUnitSucceeded).
					Updates(map[string]any{"status": model.ExtractionUnitPending, "error_message": "",
						"response_hash": "", "finished_at": nil}).Error; err != nil {
					return err
				}
				row.ProducerAttempt, row.Status, row.FailedUnitCount, row.FinishedAt =
					set.ProducerAttempt, model.RecordDraftSetRunning, 0, nil
			}
		}

		for i := range units {
			unit := &units[i]
			if unit.ID == "" {
				unit.ID = uuid.NewString()
			}
			if unit.CreatedAt.IsZero() {
				unit.CreatedAt = time.Now()
			}
			result := tx.Exec(`INSERT INTO extraction_units
				(id, record_draft_set_id, unit_key, parsed_document_id, ordinal,
				 first_block_ordinal, last_block_ordinal, input_hash, status, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (record_draft_set_id, unit_key) DO NOTHING`, unit.ID, row.ID,
				unit.UnitKey, unit.ParsedDocumentID, unit.Ordinal, unit.FirstBlockOrdinal,
				unit.LastBlockOrdinal, unit.InputHash, model.ExtractionUnitPending, unit.CreatedAt)
			if result.Error != nil {
				return result.Error
			}
			var existing extractionUnitRow
			if err := tx.Where("record_draft_set_id = ? AND unit_key = ?", row.ID, unit.UnitKey).
				First(&existing).Error; err != nil {
				return err
			}
			if existing.ParsedDocumentID != unit.ParsedDocumentID || existing.Ordinal != unit.Ordinal ||
				existing.FirstBlockOrdinal != unit.FirstBlockOrdinal ||
				existing.LastBlockOrdinal != unit.LastBlockOrdinal || existing.InputHash != unit.InputHash {
				return fmt.Errorf("抽取单元 %s 的不可变输入发生变化", unit.UnitKey)
			}
		}
		if err := refreshRecordDraftSetCounts(tx, &row); err != nil {
			return err
		}
		stored = row.toModel()
		return nil
	})
	return &stored, err
}

func (r *PipelineRepo) GetRecordDraftSet(ctx context.Context, id string) (*model.RecordDraftSet, []model.ExtractionUnit, error) {
	return r.getRecordDraftSet(ctx, "id = ?", id)
}

func (r *PipelineRepo) GetRecordDraftSetByNodeRun(ctx context.Context, nodeRunID string) (*model.RecordDraftSet, []model.ExtractionUnit, error) {
	return r.getRecordDraftSet(ctx, "producer_node_run_id = ?", nodeRunID)
}

func (r *PipelineRepo) getRecordDraftSet(ctx context.Context, query string, value any) (*model.RecordDraftSet, []model.ExtractionUnit, error) {
	var row recordDraftSetRow
	if err := r.db.WithContext(ctx).Where(query, value).First(&row).Error; err != nil {
		return nil, nil, err
	}
	var unitRows []extractionUnitRow
	if err := r.db.WithContext(ctx).Where("record_draft_set_id = ?", row.ID).
		Order("ordinal ASC").Find(&unitRows).Error; err != nil {
		return nil, nil, err
	}
	units := make([]model.ExtractionUnit, len(unitRows))
	for i := range unitRows {
		units[i] = unitRows[i].toModel()
	}
	set := row.toModel()
	return &set, units, nil
}

func (r *PipelineRepo) StartExtractionUnit(ctx context.Context, setID string, producerAttempt int, unitKey string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		set, err := lockWritableRecordDraftSet(tx, setID, producerAttempt)
		if err != nil {
			return err
		}
		var unit extractionUnitRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("record_draft_set_id = ? AND unit_key = ?", set.ID, unitKey).First(&unit).Error; err != nil {
			return err
		}
		if unit.Status == model.ExtractionUnitSucceeded {
			return nil
		}
		return tx.Model(&extractionUnitRow{}).Where("id = ?", unit.ID).Updates(map[string]any{
			"status": model.ExtractionUnitRunning, "error_message": "",
			"response_hash": "", "finished_at": nil,
		}).Error
	})
}

func (r *PipelineRepo) CompleteExtractionUnit(ctx context.Context, setID string, producerAttempt int, unitKey, responseHash string, usage model.LLMUsage, drafts []model.RecordDraft) error {
	if strings.TrimSpace(responseHash) == "" {
		return fmt.Errorf("response_hash 不能为空")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		set, err := lockWritableRecordDraftSet(tx, setID, producerAttempt)
		if err != nil {
			return err
		}
		var unit extractionUnitRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("record_draft_set_id = ? AND unit_key = ?", set.ID, unitKey).First(&unit).Error; err != nil {
			return err
		}
		if unit.Status == model.ExtractionUnitSucceeded {
			if unit.ResponseHash == responseHash {
				return nil
			}
			return fmt.Errorf("抽取单元 %s 已由不同响应完成", unitKey)
		}
		if err := tx.Where("extraction_unit_id = ?", unit.ID).Delete(&recordDraftRow{}).Error; err != nil {
			return err
		}
		now := time.Now()
		for i := range drafts {
			draft := &drafts[i]
			// 与 record_drafts 的 jsonb_typeof='object' CHECK 对齐：非法值在这里报可归因的
			// 应用层错误，而不是等 INSERT 撞数据库约束后炸出 SQLSTATE 23514。
			for _, column := range []struct {
				name  string
				value json.RawMessage
			}{
				{"fields", draft.Fields}, {"field_confidence", draft.FieldConfidence},
			} {
				if err := requireJSONBObject(column.name, column.value); err != nil {
					return fmt.Errorf("抽取单元 %s 的第 %d 条草稿: %w", unitKey, i+1, err)
				}
			}
			provenance, err := json.Marshal(draft.Provenance)
			if err != nil {
				return fmt.Errorf("序列化草稿来源: %w", err)
			}
			if draft.ID == "" {
				draft.ID = uuid.NewString()
			}
			draft.RecordDraftSetID, draft.ExtractionUnitID = set.ID, unit.ID
			draft.Ordinal = i
			if draft.CreatedAt.IsZero() {
				draft.CreatedAt = now
			}
			if err := tx.Exec(`INSERT INTO record_drafts
				(id, record_draft_set_id, extraction_unit_id, ordinal, fields,
				 field_confidence, provenance, created_at)
				VALUES (?, ?, ?, ?, ?::jsonb, ?::jsonb, ?::jsonb, ?)`, draft.ID, set.ID,
				unit.ID, draft.Ordinal, string(draft.Fields), string(draft.FieldConfidence),
				string(provenance), draft.CreatedAt).Error; err != nil {
				return err
			}
		}
		updates := extractionUnitUsageUpdates(producerAttempt, usage)
		updates["status"] = model.ExtractionUnitSucceeded
		updates["error_message"] = ""
		updates["response_hash"] = responseHash
		updates["finished_at"] = now
		return tx.Model(&extractionUnitRow{}).Where("id = ?", unit.ID).Updates(updates).Error
	})
}

func (r *PipelineRepo) FailExtractionUnit(ctx context.Context, setID string, producerAttempt int, unitKey, message string, usage model.LLMUsage) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		set, err := lockWritableRecordDraftSet(tx, setID, producerAttempt)
		if err != nil {
			return err
		}
		var unit extractionUnitRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("record_draft_set_id = ? AND unit_key = ?", set.ID, unitKey).First(&unit).Error; err != nil {
			return err
		}
		if unit.Status == model.ExtractionUnitSucceeded {
			return nil
		}
		now := time.Now()
		updates := extractionUnitUsageUpdates(producerAttempt, usage)
		updates["status"] = model.ExtractionUnitFailed
		updates["error_message"] = strings.TrimSpace(message)
		updates["response_hash"] = ""
		updates["finished_at"] = now
		return tx.Model(&extractionUnitRow{}).Where("id = ?", unit.ID).Updates(updates).Error
	})
}

func extractionUnitUsageUpdates(producerAttempt int, usage model.LLMUsage) map[string]any {
	return map[string]any{
		"request_count": gorm.Expr(`request_count + CASE
			WHEN last_request_attempt < ? THEN ? ELSE 0 END`, producerAttempt, usage.RequestCount),
		"last_request_attempt": gorm.Expr("GREATEST(last_request_attempt, ?)", producerAttempt),
		"input_tokens": gorm.Expr(`input_tokens + CASE
			WHEN last_usage_attempt < ? THEN ? ELSE 0 END`, producerAttempt, usage.InputTokens),
		"output_tokens": gorm.Expr(`output_tokens + CASE
			WHEN last_usage_attempt < ? THEN ? ELSE 0 END`, producerAttempt, usage.OutputTokens),
		"cache_read_tokens": gorm.Expr(`cache_read_tokens + CASE
			WHEN last_usage_attempt < ? THEN ? ELSE 0 END`, producerAttempt, usage.CacheReadTokens),
		"cache_write_tokens": gorm.Expr(`cache_write_tokens + CASE
			WHEN last_usage_attempt < ? THEN ? ELSE 0 END`, producerAttempt, usage.CacheWriteTokens),
		"last_usage_attempt": gorm.Expr("GREATEST(last_usage_attempt, ?)", producerAttempt),
	}
}

func (r *PipelineRepo) FinalizeRecordDraftSet(ctx context.Context, setID string, producerAttempt int) (*model.RecordDraftSet, error) {
	var completed model.RecordDraftSet
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := lockWritableRecordDraftSet(tx, setID, producerAttempt)
		if err != nil {
			return err
		}
		var counts struct {
			Total      int
			Succeeded  int
			Failed     int
			Pending    int
			Drafts     int
			Requests   int
			Input      int
			Output     int
			CacheRead  int
			CacheWrite int
		}
		if err := tx.Raw(`SELECT count(*) AS total,
			count(*) FILTER (WHERE status = ?) AS succeeded,
			count(*) FILTER (WHERE status = ?) AS failed,
			count(*) FILTER (WHERE status NOT IN (?, ?)) AS pending,
			(SELECT count(*) FROM record_drafts WHERE record_draft_set_id = ?) AS drafts,
			COALESCE(sum(request_count), 0) AS requests,
			COALESCE(sum(input_tokens), 0) AS input,
			COALESCE(sum(output_tokens), 0) AS output,
			COALESCE(sum(cache_read_tokens), 0) AS cache_read,
			COALESCE(sum(cache_write_tokens), 0) AS cache_write
			FROM extraction_units WHERE record_draft_set_id = ?`, model.ExtractionUnitSucceeded,
			model.ExtractionUnitFailed, model.ExtractionUnitSucceeded, model.ExtractionUnitFailed,
			setID, setID).Scan(&counts).Error; err != nil {
			return err
		}
		if counts.Pending != 0 || counts.Total != row.UnitCount {
			return fmt.Errorf("RecordDraftSet 尚有 %d 个抽取单元未完成", counts.Pending)
		}
		status := model.RecordDraftSetPartial
		if counts.Total == 0 {
			status = model.RecordDraftSetFailed
		} else if counts.Failed == 0 {
			status = model.RecordDraftSetSucceeded
		} else if counts.Succeeded == 0 {
			status = model.RecordDraftSetFailed
		}
		now := time.Now()
		if err := tx.Model(&recordDraftSetRow{}).Where("id = ?", setID).Updates(map[string]any{
			"status": status, "succeeded_unit_count": counts.Succeeded,
			"failed_unit_count": counts.Failed, "draft_count": counts.Drafts,
			"llm_request_count": counts.Requests, "input_tokens": counts.Input,
			"output_tokens": counts.Output, "cache_read_tokens": counts.CacheRead,
			"cache_write_tokens": counts.CacheWrite,
			"finished_at":        now,
		}).Error; err != nil {
			return err
		}
		row.Status, row.SucceededUnitCount, row.FailedUnitCount, row.DraftCount =
			status, counts.Succeeded, counts.Failed, counts.Drafts
		row.LLMRequestCount, row.InputTokens, row.OutputTokens = counts.Requests, counts.Input, counts.Output
		row.CacheReadTokens, row.CacheWriteTokens, row.FinishedAt = counts.CacheRead, counts.CacheWrite, &now
		completed = row.toModel()
		return nil
	})
	return &completed, err
}

func (r *PipelineRepo) ListRecordDrafts(ctx context.Context, setID string) ([]model.RecordDraft, error) {
	var rows []recordDraftRow
	if err := r.db.WithContext(ctx).Table("record_drafts AS d").Select("d.*").
		Joins("JOIN extraction_units AS u ON u.id = d.extraction_unit_id").
		Where("d.record_draft_set_id = ?", setID).
		Order("u.ordinal ASC, d.ordinal ASC").Scan(&rows).Error; err != nil {
		return nil, err
	}
	drafts := make([]model.RecordDraft, len(rows))
	for i := range rows {
		item, err := rows[i].toModel()
		if err != nil {
			return nil, err
		}
		drafts[i] = item
	}
	return drafts, nil
}

func assertActiveNodeProducer(tx *gorm.DB, nodeRunID string, attempt int) error {
	var producer struct {
		Attempt    int
		Status     string
		LeaseUntil *time.Time
	}
	if err := tx.Raw(`SELECT attempt, status, lease_until FROM workflow_node_runs WHERE id = ? FOR UPDATE`,
		nodeRunID).Scan(&producer).Error; err != nil {
		return err
	}
	if producer.Attempt != attempt || producer.Status != "running" ||
		producer.LeaseUntil == nil || !producer.LeaseUntil.After(time.Now()) {
		return port.ErrStaleResourceExecution
	}
	return nil
}

func lockWritableRecordDraftSet(tx *gorm.DB, setID string, attempt int) (*recordDraftSetRow, error) {
	// 所有写路径统一先锁 NodeRun 再锁 Manifest，避免锁顺序反转。
	var identity struct {
		ProducerNodeRunID string
	}
	if err := tx.Model(&recordDraftSetRow{}).Select("producer_node_run_id").
		Where("id = ?", setID).Take(&identity).Error; err != nil {
		return nil, err
	}
	if err := assertActiveNodeProducer(tx, identity.ProducerNodeRunID, attempt); err != nil {
		return nil, err
	}
	var row recordDraftSetRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", setID).First(&row).Error; err != nil {
		return nil, err
	}
	if row.ProducerNodeRunID != identity.ProducerNodeRunID || row.ProducerAttempt != attempt ||
		row.Status != model.RecordDraftSetRunning {
		return nil, port.ErrStaleResourceExecution
	}
	return &row, nil
}

func refreshRecordDraftSetCounts(tx *gorm.DB, row *recordDraftSetRow) error {
	var counts struct {
		Succeeded  int
		Failed     int
		Drafts     int
		Requests   int
		Input      int
		Output     int
		CacheRead  int
		CacheWrite int
	}
	if err := tx.Raw(`SELECT
		count(*) FILTER (WHERE status = ?) AS succeeded,
		count(*) FILTER (WHERE status = ?) AS failed,
		(SELECT count(*) FROM record_drafts WHERE record_draft_set_id = ?) AS drafts,
		COALESCE(sum(request_count), 0) AS requests,
		COALESCE(sum(input_tokens), 0) AS input,
		COALESCE(sum(output_tokens), 0) AS output,
		COALESCE(sum(cache_read_tokens), 0) AS cache_read,
		COALESCE(sum(cache_write_tokens), 0) AS cache_write
		FROM extraction_units WHERE record_draft_set_id = ?`, model.ExtractionUnitSucceeded,
		model.ExtractionUnitFailed, row.ID, row.ID).Scan(&counts).Error; err != nil {
		return err
	}
	if err := tx.Model(&recordDraftSetRow{}).Where("id = ?", row.ID).Updates(map[string]any{
		"succeeded_unit_count": counts.Succeeded, "failed_unit_count": counts.Failed,
		"draft_count": counts.Drafts, "llm_request_count": counts.Requests,
		"input_tokens": counts.Input, "output_tokens": counts.Output,
		"cache_read_tokens": counts.CacheRead, "cache_write_tokens": counts.CacheWrite,
	}).Error; err != nil {
		return err
	}
	row.SucceededUnitCount, row.FailedUnitCount, row.DraftCount = counts.Succeeded, counts.Failed, counts.Drafts
	row.LLMRequestCount, row.InputTokens, row.OutputTokens = counts.Requests, counts.Input, counts.Output
	row.CacheReadTokens, row.CacheWriteTokens = counts.CacheRead, counts.CacheWrite
	return nil
}

type recordDraftSetRow struct {
	ID                  string          `gorm:"column:id;primaryKey"`
	ParsedDocumentSetID string          `gorm:"column:parsed_document_set_id"`
	DataContract        json.RawMessage `gorm:"column:data_contract;type:jsonb"`
	DataContractHash    string          `gorm:"column:data_contract_hash"`
	ExtractionSpec      json.RawMessage `gorm:"column:extraction_spec;type:jsonb"`
	ExtractionSpecHash  string          `gorm:"column:extraction_spec_hash"`
	JSONSchema          json.RawMessage `gorm:"column:json_schema;type:jsonb"`
	SchemaHash          string          `gorm:"column:schema_hash"`
	ProducerNodeRunID   string          `gorm:"column:producer_node_run_id"`
	Status              string          `gorm:"column:status"`
	ProducerAttempt     int             `gorm:"column:producer_attempt"`
	Model               string          `gorm:"column:model"`
	UnitCount           int             `gorm:"column:unit_count"`
	SucceededUnitCount  int             `gorm:"column:succeeded_unit_count"`
	FailedUnitCount     int             `gorm:"column:failed_unit_count"`
	DraftCount          int             `gorm:"column:draft_count"`
	LLMRequestCount     int             `gorm:"column:llm_request_count"`
	InputTokens         int             `gorm:"column:input_tokens"`
	OutputTokens        int             `gorm:"column:output_tokens"`
	CacheReadTokens     int             `gorm:"column:cache_read_tokens"`
	CacheWriteTokens    int             `gorm:"column:cache_write_tokens"`
	CreatedAt           time.Time       `gorm:"column:created_at"`
	FinishedAt          *time.Time      `gorm:"column:finished_at"`
}

func (recordDraftSetRow) TableName() string { return "record_draft_sets" }
func (row recordDraftSetRow) toModel() model.RecordDraftSet {
	set := model.RecordDraftSet{ID: row.ID, ParsedDocumentSetID: row.ParsedDocumentSetID,
		DataContract: row.DataContract, DataContractHash: row.DataContractHash,
		ExtractionSpec: row.ExtractionSpec, ExtractionSpecHash: row.ExtractionSpecHash,
		JSONSchema: row.JSONSchema, SchemaHash: row.SchemaHash, ProducerNodeRunID: row.ProducerNodeRunID,
		Status: row.Status, ProducerAttempt: row.ProducerAttempt, Model: row.Model,
		UnitCount: row.UnitCount, SucceededUnitCount: row.SucceededUnitCount,
		FailedUnitCount: row.FailedUnitCount, DraftCount: row.DraftCount,
		LLMRequestCount: row.LLMRequestCount, InputTokens: row.InputTokens,
		OutputTokens: row.OutputTokens, CacheReadTokens: row.CacheReadTokens,
		CacheWriteTokens: row.CacheWriteTokens, CreatedAt: row.CreatedAt}
	if row.FinishedAt != nil {
		set.FinishedAt = *row.FinishedAt
	}
	return set
}

type extractionUnitRow struct {
	ID                 string     `gorm:"column:id;primaryKey"`
	RecordDraftSetID   string     `gorm:"column:record_draft_set_id"`
	UnitKey            string     `gorm:"column:unit_key"`
	ParsedDocumentID   string     `gorm:"column:parsed_document_id"`
	Ordinal            int        `gorm:"column:ordinal"`
	FirstBlockOrdinal  int        `gorm:"column:first_block_ordinal"`
	LastBlockOrdinal   int        `gorm:"column:last_block_ordinal"`
	InputHash          string     `gorm:"column:input_hash"`
	Status             string     `gorm:"column:status"`
	ErrorMessage       string     `gorm:"column:error_message"`
	ResponseHash       string     `gorm:"column:response_hash"`
	RequestCount       int        `gorm:"column:request_count"`
	LastRequestAttempt int        `gorm:"column:last_request_attempt"`
	LastUsageAttempt   int        `gorm:"column:last_usage_attempt"`
	InputTokens        int        `gorm:"column:input_tokens"`
	OutputTokens       int        `gorm:"column:output_tokens"`
	CacheReadTokens    int        `gorm:"column:cache_read_tokens"`
	CacheWriteTokens   int        `gorm:"column:cache_write_tokens"`
	CreatedAt          time.Time  `gorm:"column:created_at"`
	FinishedAt         *time.Time `gorm:"column:finished_at"`
}

func (extractionUnitRow) TableName() string { return "extraction_units" }
func (row extractionUnitRow) toModel() model.ExtractionUnit {
	unit := model.ExtractionUnit{ID: row.ID, RecordDraftSetID: row.RecordDraftSetID,
		UnitKey: row.UnitKey, ParsedDocumentID: row.ParsedDocumentID, Ordinal: row.Ordinal,
		FirstBlockOrdinal: row.FirstBlockOrdinal, LastBlockOrdinal: row.LastBlockOrdinal,
		InputHash: row.InputHash, Status: row.Status, ErrorMessage: row.ErrorMessage,
		ResponseHash: row.ResponseHash, RequestCount: row.RequestCount,
		InputTokens: row.InputTokens, OutputTokens: row.OutputTokens,
		CacheReadTokens: row.CacheReadTokens, CacheWriteTokens: row.CacheWriteTokens,
		CreatedAt: row.CreatedAt}
	if row.FinishedAt != nil {
		unit.FinishedAt = *row.FinishedAt
	}
	return unit
}

type recordDraftRow struct {
	ID               string          `gorm:"column:id;primaryKey"`
	RecordDraftSetID string          `gorm:"column:record_draft_set_id"`
	ExtractionUnitID string          `gorm:"column:extraction_unit_id"`
	Ordinal          int             `gorm:"column:ordinal"`
	Fields           json.RawMessage `gorm:"column:fields;type:jsonb"`
	FieldConfidence  json.RawMessage `gorm:"column:field_confidence;type:jsonb"`
	Provenance       json.RawMessage `gorm:"column:provenance;type:jsonb"`
	CreatedAt        time.Time       `gorm:"column:created_at"`
}

func (recordDraftRow) TableName() string { return "record_drafts" }
func (row recordDraftRow) toModel() (model.RecordDraft, error) {
	var provenance model.ItemProvenance
	if err := json.Unmarshal(row.Provenance, &provenance); err != nil {
		return model.RecordDraft{}, fmt.Errorf("解析 RecordDraft %s provenance: %w", row.ID, err)
	}
	return model.RecordDraft{ID: row.ID, RecordDraftSetID: row.RecordDraftSetID,
		ExtractionUnitID: row.ExtractionUnitID, Ordinal: row.Ordinal, Fields: row.Fields,
		FieldConfidence: row.FieldConfidence, Provenance: provenance, CreatedAt: row.CreatedAt}, nil
}
