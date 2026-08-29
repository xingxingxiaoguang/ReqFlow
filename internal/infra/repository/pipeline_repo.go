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

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// PipelineRepo 实现 V2 不可变 Schema 与追加型 Dataset 的持久化边界。
type PipelineRepo struct {
	db *gorm.DB
}

func NewPipelineRepo(db *gorm.DB) *PipelineRepo { return &PipelineRepo{db: db} }

func (r *PipelineRepo) CreateDatasetSchema(ctx context.Context, schema *model.DatasetSchemaDefinition) error {
	if schema.ID == "" {
		schema.ID = uuid.NewString()
	}
	schema.CreatedAt = time.Now()
	return r.db.WithContext(ctx).Exec(`INSERT INTO dataset_schemas
		(id, workspace_id, name, description, json_schema, ui_schema, schema_hash, created_at)
		VALUES (?, ?, ?, ?, ?::jsonb, ?::jsonb, ?, ?)`,
		schema.ID, schema.WorkspaceID, schema.Name, schema.Description,
		string(schema.JSONSchema), string(schema.UISchema), schema.SchemaHash, schema.CreatedAt).Error
}

func (r *PipelineRepo) GetDatasetSchema(ctx context.Context, id string) (*model.DatasetSchemaDefinition, error) {
	var row pipelineSchemaRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	return row.toModel(), nil
}

func (r *PipelineRepo) CreateAppendDataset(ctx context.Context, dataset *model.Dataset) error {
	if dataset.ID == "" {
		dataset.ID = uuid.NewString()
	}
	now := time.Now()
	dataset.CreatedAt, dataset.UpdatedAt = now, now
	keyFields, err := json.Marshal(dataset.KeyFields)
	if err != nil {
		return fmt.Errorf("序列化 key_fields: %w", err)
	}
	return r.db.WithContext(ctx).Exec(`INSERT INTO datasets
		(id, workspace_id, type, purpose, name, schema_id, schema, key_fields,
		 description, tags, source_task_id, status, item_count, current_seq,
		 schema_version, extra, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?::jsonb, ?::jsonb, ?, '[]', NULL, ?, 0, 0, 1, '{}', ?, ?)`,
		dataset.ID, dataset.WorkspaceID, dataset.Type, dataset.Purpose, dataset.Name,
		dataset.SchemaID, dataset.Schema, string(keyFields), dataset.Description,
		dataset.Status, dataset.CreatedAt, dataset.UpdatedAt).Error
}

func (r *PipelineRepo) GetAppendDataset(ctx context.Context, id string) (*model.Dataset, error) {
	var row pipelineDatasetRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	return row.toModel(), nil
}

func (r *PipelineRepo) CreateDatasetBatch(ctx context.Context, batch *model.DatasetBatch) error {
	if batch.ID == "" {
		batch.ID = uuid.NewString()
	}
	batch.CreatedAt = time.Now()
	return r.db.WithContext(ctx).Exec(`INSERT INTO dataset_batches
		(id, dataset_id, source_task_id, source_step_run_id, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, batch.ID, batch.DatasetID,
		nullableUUID(batch.SourceTaskID), nullableUUID(batch.SourceStepRunID), batch.Status, batch.CreatedAt).Error
}

func (r *PipelineRepo) GetOrCreateDatasetBatchForStep(ctx context.Context, batch *model.DatasetBatch,
	producerAttempt int) (*model.DatasetBatch, error) {
	if batch == nil || strings.TrimSpace(batch.DatasetID) == "" || strings.TrimSpace(batch.SourceStepRunID) == "" {
		return nil, fmt.Errorf("幂等 Batch 必须提供 dataset_id 和 source_step_run_id")
	}
	if batch.ID == "" {
		batch.ID = uuid.NewString()
	}
	if batch.CreatedAt.IsZero() {
		batch.CreatedAt = time.Now()
	}
	if batch.Status == "" {
		batch.Status = model.DatasetBatchStaging
	}
	var stored model.DatasetBatch
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assertActiveStepProducer(tx, batch.SourceStepRunID, producerAttempt); err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO dataset_batches
			(id, dataset_id, source_task_id, source_step_run_id, status, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (source_step_run_id) WHERE source_step_run_id IS NOT NULL DO NOTHING`,
			batch.ID, batch.DatasetID, nullableUUID(batch.SourceTaskID), batch.SourceStepRunID,
			batch.Status, batch.CreatedAt).Error; err != nil {
			return err
		}
		var row pipelineBatchRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("source_step_run_id = ?", batch.SourceStepRunID).First(&row).Error; err != nil {
			return err
		}
		if row.DatasetID != batch.DatasetID || strVal(row.SourceTaskID) != batch.SourceTaskID {
			return fmt.Errorf("StepRun %s 已绑定到不同的 Dataset Batch", batch.SourceStepRunID)
		}
		stored = *row.toModel()
		return nil
	})
	return &stored, err
}

func (r *PipelineRepo) GetDatasetBatch(ctx context.Context, id string) (*model.DatasetBatch, error) {
	var row pipelineBatchRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	return row.toModel(), nil
}

func (r *PipelineRepo) CommitDatasetBatch(ctx context.Context, batchID, payloadHash string, items []model.DatasetItem) (*model.DatasetBatch, error) {
	return r.commitDatasetBatch(ctx, batchID, payloadHash, items, nil)
}

func (r *PipelineRepo) CommitDatasetBatchForStep(ctx context.Context, batchID, sourceStepRunID string,
	producerAttempt int, payloadHash string, items []model.DatasetItem) (*model.DatasetBatch, error) {
	return r.commitDatasetBatch(ctx, batchID, payloadHash, items, func(tx *gorm.DB, batch *pipelineBatchRow) error {
		if batch.SourceStepRunID == nil || *batch.SourceStepRunID != sourceStepRunID {
			return port.ErrStaleResourceExecution
		}
		return assertActiveStepProducer(tx, sourceStepRunID, producerAttempt)
	})
}

func (r *PipelineRepo) commitDatasetBatch(ctx context.Context, batchID, payloadHash string,
	items []model.DatasetItem, fence func(*gorm.DB, *pipelineBatchRow) error) (*model.DatasetBatch, error) {
	var committed *model.DatasetBatch
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var batch pipelineBatchRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", batchID).First(&batch).Error; err != nil {
			return err
		}
		if fence != nil {
			if err := fence(tx, &batch); err != nil {
				return err
			}
		}
		if batch.Status == model.DatasetBatchCommitted {
			if batch.PayloadHash != payloadHash {
				return fmt.Errorf("%w: 已提交 Batch 的 payload_hash 不一致", port.ErrDatasetBatchNotWritable)
			}
			committed = batch.toModel()
			return nil
		}
		if batch.Status != model.DatasetBatchStaging && batch.Status != model.DatasetBatchValidating {
			return fmt.Errorf("%w: 当前状态 %s", port.ErrDatasetBatchNotWritable, batch.Status)
		}

		var dataset pipelineDatasetRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", batch.DatasetID).First(&dataset).Error; err != nil {
			return err
		}
		if dataset.Status != model.DatasetStatusActive {
			return fmt.Errorf("Dataset %s 当前状态 %s 不允许追加", dataset.ID, dataset.Status)
		}
		from, to, err := logic.NextCommitRange(dataset.CurrentSeq, len(items))
		if err != nil {
			return err
		}

		keys := make([]string, len(items))
		for i, item := range items {
			keys[i] = item.ItemKey
		}
		var conflict string
		if err := tx.Model(&datasetItemRow{}).Select("item_key").
			Where("dataset_id = ? AND item_key IN ?", dataset.ID, keys).
			Limit(1).Scan(&conflict).Error; err != nil {
			return err
		}
		if conflict != "" {
			return fmt.Errorf("%w: %s", port.ErrDatasetItemKeyConflict, conflict)
		}

		now := time.Now()
		for i := range items {
			item := &items[i]
			if item.ID == "" {
				item.ID = uuid.NewString()
			}
			item.DatasetID = dataset.ID
			item.BatchID = batch.ID
			item.CommitSeq = from + int64(i)
			item.CreatedAt, item.UpdatedAt = now, now
			if strings.TrimSpace(item.Provenance) == "" {
				item.Provenance = "{}"
			}
			if err := tx.Exec(`INSERT INTO dataset_items
				(id, dataset_id, batch_id, fields, item_key, fingerprint, source_task_id,
				 commit_seq, provenance, created_at, updated_at)
				VALUES (?, ?, ?, ?::jsonb, ?, ?, ?, ?, ?::jsonb, ?, ?)`,
				item.ID, item.DatasetID, item.BatchID, item.Fields, item.ItemKey,
				item.Fingerprint, nullableUUID(item.SourceTaskID), item.CommitSeq,
				item.Provenance, item.CreatedAt, item.UpdatedAt).Error; err != nil {
				return err
			}
		}

		if err := tx.Model(&pipelineDatasetRow{}).Where("id = ?", dataset.ID).
			Updates(map[string]any{
				"current_seq": to,
				"item_count":  gorm.Expr("item_count + ?", len(items)),
				"updated_at":  now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&pipelineBatchRow{}).Where("id = ?", batch.ID).
			Updates(map[string]any{
				"status": model.DatasetBatchCommitted, "item_count": len(items),
				"from_seq": from, "to_seq": to, "payload_hash": payloadHash,
				"committed_at": now,
			}).Error; err != nil {
			return err
		}

		payload, _ := json.Marshal(map[string]any{
			"dataset_id": dataset.ID, "batch_id": batch.ID,
			"from_seq": from, "to_seq": to, "item_count": len(items),
		})
		if err := tx.Exec(`INSERT INTO outbox_events
			(id, topic, aggregate_type, aggregate_id, payload)
			VALUES (?, 'dataset.batch_committed', 'dataset', ?, ?::jsonb)`,
			uuid.NewString(), dataset.ID, string(payload)).Error; err != nil {
			return err
		}

		batch.Status = model.DatasetBatchCommitted
		batch.ItemCount = len(items)
		batch.FromSeq, batch.ToSeq = from, to
		batch.PayloadHash = payloadHash
		batch.CommittedAt = &now
		committed = batch.toModel()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return committed, nil
}

func (r *PipelineRepo) ListDatasetItemsAfter(ctx context.Context, datasetID string, afterSeq, throughSeq int64, limit int) ([]model.DatasetItem, error) {
	if throughSeq <= afterSeq {
		return []model.DatasetItem{}, nil
	}
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	var rows []pipelineDatasetItemRow
	if err := r.db.WithContext(ctx).
		Where("dataset_id = ? AND commit_seq > ? AND commit_seq <= ?", datasetID, afterSeq, throughSeq).
		Order("commit_seq ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.DatasetItem, len(rows))
	for i := range rows {
		out[i] = rows[i].toModel()
	}
	return out, nil
}

type pipelineSchemaRow struct {
	ID          string    `gorm:"column:id;primaryKey"`
	WorkspaceID string    `gorm:"column:workspace_id"`
	Name        string    `gorm:"column:name"`
	Description string    `gorm:"column:description"`
	JSONSchema  string    `gorm:"column:json_schema;type:jsonb"`
	UISchema    string    `gorm:"column:ui_schema;type:jsonb"`
	SchemaHash  string    `gorm:"column:schema_hash"`
	CreatedAt   time.Time `gorm:"column:created_at"`
}

func (pipelineSchemaRow) TableName() string { return "dataset_schemas" }

func (row pipelineSchemaRow) toModel() *model.DatasetSchemaDefinition {
	return &model.DatasetSchemaDefinition{
		ID: row.ID, WorkspaceID: row.WorkspaceID, Name: row.Name, Description: row.Description,
		JSONSchema: json.RawMessage(row.JSONSchema), UISchema: json.RawMessage(row.UISchema),
		SchemaHash: row.SchemaHash, CreatedAt: row.CreatedAt,
	}
}

type pipelineDatasetRow struct {
	ID          string               `gorm:"column:id;primaryKey"`
	WorkspaceID string               `gorm:"column:workspace_id"`
	Type        string               `gorm:"column:type"`
	Purpose     model.DatasetPurpose `gorm:"column:purpose"`
	Name        string               `gorm:"column:name"`
	SchemaID    string               `gorm:"column:schema_id"`
	Schema      string               `gorm:"column:schema;type:jsonb"`
	KeyFields   string               `gorm:"column:key_fields;type:jsonb"`
	Description string               `gorm:"column:description"`
	Status      string               `gorm:"column:status"`
	ItemCount   int                  `gorm:"column:item_count"`
	CurrentSeq  int64                `gorm:"column:current_seq"`
	CreatedAt   time.Time            `gorm:"column:created_at"`
	UpdatedAt   time.Time            `gorm:"column:updated_at"`
}

func (pipelineDatasetRow) TableName() string { return "datasets" }

func (row pipelineDatasetRow) toModel() *model.Dataset {
	var keyFields []string
	_ = json.Unmarshal([]byte(row.KeyFields), &keyFields)
	return &model.Dataset{
		ID: row.ID, WorkspaceID: row.WorkspaceID, Type: row.Type, Purpose: row.Purpose,
		Name: row.Name, SchemaID: row.SchemaID, Schema: row.Schema, KeyFields: keyFields,
		Description: row.Description, Status: row.Status, ItemCount: row.ItemCount,
		CurrentSeq: row.CurrentSeq, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

type pipelineBatchRow struct {
	ID              string     `gorm:"column:id;primaryKey"`
	DatasetID       string     `gorm:"column:dataset_id"`
	SourceTaskID    *string    `gorm:"column:source_task_id"`
	SourceStepRunID *string    `gorm:"column:source_step_run_id"`
	Status          string     `gorm:"column:status"`
	ItemCount       int        `gorm:"column:item_count"`
	FromSeq         int64      `gorm:"column:from_seq"`
	ToSeq           int64      `gorm:"column:to_seq"`
	PayloadHash     string     `gorm:"column:payload_hash"`
	ErrorMessage    string     `gorm:"column:error_message"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	CommittedAt     *time.Time `gorm:"column:committed_at"`
}

func (pipelineBatchRow) TableName() string { return "dataset_batches" }

func (row pipelineBatchRow) toModel() *model.DatasetBatch {
	batch := &model.DatasetBatch{
		ID: row.ID, DatasetID: row.DatasetID, SourceTaskID: strVal(row.SourceTaskID),
		SourceStepRunID: strVal(row.SourceStepRunID), Status: row.Status,
		ItemCount: row.ItemCount, FromSeq: row.FromSeq, ToSeq: row.ToSeq,
		PayloadHash: row.PayloadHash, ErrorMessage: row.ErrorMessage, CreatedAt: row.CreatedAt,
	}
	if row.CommittedAt != nil {
		batch.CommittedAt = *row.CommittedAt
	}
	return batch
}

type pipelineDatasetItemRow struct {
	ID           string    `gorm:"column:id;primaryKey"`
	DatasetID    string    `gorm:"column:dataset_id"`
	BatchID      string    `gorm:"column:batch_id"`
	Fields       string    `gorm:"column:fields;type:jsonb"`
	ItemKey      string    `gorm:"column:item_key"`
	Fingerprint  string    `gorm:"column:fingerprint"`
	CommitSeq    int64     `gorm:"column:commit_seq"`
	Provenance   string    `gorm:"column:provenance;type:jsonb"`
	SourceTaskID *string   `gorm:"column:source_task_id"`
	CreatedAt    time.Time `gorm:"column:created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at"`
}

func (pipelineDatasetItemRow) TableName() string { return "dataset_items" }

func (row pipelineDatasetItemRow) toModel() model.DatasetItem {
	return model.DatasetItem{
		ID: row.ID, DatasetID: row.DatasetID, BatchID: row.BatchID,
		Fields: row.Fields, ItemKey: row.ItemKey, Fingerprint: row.Fingerprint,
		CommitSeq: row.CommitSeq, Provenance: row.Provenance, SourceTaskID: strVal(row.SourceTaskID),
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func nullableUUID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
