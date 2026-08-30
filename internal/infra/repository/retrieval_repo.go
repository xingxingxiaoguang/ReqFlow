package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	pgvector "github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

func (r *PipelineRepo) CreateRetrievalProfile(ctx context.Context, profile *model.RetrievalProfile) error {
	if profile.ID == "" {
		profile.ID = uuid.NewString()
	}
	profile.CreatedAt = time.Now()
	lexical, err := json.Marshal(profile.Lexical)
	if err != nil {
		return err
	}
	vector, err := json.Marshal(profile.Vector)
	if err != nil {
		return err
	}
	filters, err := json.Marshal(profile.FilterFields)
	if err != nil {
		return err
	}
	fusion, err := json.Marshal(profile.Fusion)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Exec(`INSERT INTO retrieval_profiles
		(id, workspace_id, name, dataset_schema_id, lexical_config, vector_config,
		 filter_fields, fusion_config, profile_hash, created_at)
		VALUES (?, ?, ?, ?, ?::jsonb, ?::jsonb, ?::jsonb, ?::jsonb, ?, ?)`,
		profile.ID, profile.WorkspaceID, profile.Name, profile.DatasetSchemaID,
		string(lexical), string(vector), string(filters), string(fusion), profile.ProfileHash, profile.CreatedAt).Error
}

func (r *PipelineRepo) GetRetrievalProfile(ctx context.Context, id string) (*model.RetrievalProfile, error) {
	var row retrievalProfileRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	return row.toModel()
}

func (r *PipelineRepo) ListRetrievalProfiles(ctx context.Context, workspaceID, datasetSchemaID string,
	limit int) ([]model.RetrievalProfile, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	query := r.db.WithContext(ctx).Where("workspace_id = ?", workspaceID)
	if strings.TrimSpace(datasetSchemaID) != "" {
		query = query.Where("dataset_schema_id = ?", datasetSchemaID)
	}
	var rows []retrievalProfileRow
	if err := query.Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.RetrievalProfile, len(rows))
	for i := range rows {
		profile, err := rows[i].toModel()
		if err != nil {
			return nil, err
		}
		out[i] = *profile
	}
	return out, nil
}

func (r *PipelineRepo) GetOrCreateRetrievalSnapshotForStep(ctx context.Context, snapshot *model.RetrievalSnapshot,
	producerAttempt int) (*model.RetrievalSnapshot, error) {
	if snapshot == nil || strings.TrimSpace(snapshot.DatasetID) == "" || strings.TrimSpace(snapshot.RetrievalProfileID) == "" ||
		strings.TrimSpace(snapshot.SourceStepRunID) == "" {
		return nil, fmt.Errorf("幂等 RetrievalSnapshot 必须提供 dataset/profile/source_step_run")
	}
	if snapshot.ID == "" {
		snapshot.ID = uuid.NewString()
	}
	if snapshot.Status == "" {
		snapshot.Status = model.RetrievalSnapshotBuilding
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = time.Now()
	}
	var stored *model.RetrievalSnapshot
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assertActiveStepProducer(tx, snapshot.SourceStepRunID, producerAttempt); err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO retrieval_snapshots
			(id, dataset_id, retrieval_profile_id, source_step_run_id, producer_attempt,
			 source_seq, status, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (source_step_run_id) WHERE source_step_run_id IS NOT NULL DO NOTHING`,
			snapshot.ID, snapshot.DatasetID, snapshot.RetrievalProfileID, snapshot.SourceStepRunID,
			producerAttempt, snapshot.SourceSeq, snapshot.Status, snapshot.CreatedAt).Error; err != nil {
			return err
		}
		var row retrievalSnapshotRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_step_run_id = ?", snapshot.SourceStepRunID).
			First(&row).Error; err != nil {
			return err
		}
		if row.DatasetID != snapshot.DatasetID || row.RetrievalProfileID != snapshot.RetrievalProfileID || row.SourceSeq != snapshot.SourceSeq {
			return fmt.Errorf("StepRun %s 已绑定到不同的 RetrievalSnapshot", snapshot.SourceStepRunID)
		}
		if row.Status != model.RetrievalSnapshotActive {
			if err := tx.Model(&retrievalSnapshotRow{}).Where("id = ?", row.ID).Updates(map[string]any{
				"status": model.RetrievalSnapshotBuilding, "failure_reason": "", "producer_attempt": producerAttempt,
			}).Error; err != nil {
				return err
			}
			row.Status, row.FailureReason, row.ProducerAttempt = model.RetrievalSnapshotBuilding, "", producerAttempt
		}
		var err error
		stored, err = row.toModel()
		return err
	})
	return stored, err
}

func (r *PipelineRepo) GetRetrievalSnapshot(ctx context.Context, id string) (*model.RetrievalSnapshot, error) {
	var row retrievalSnapshotRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	return row.toModel()
}

func (r *PipelineRepo) ListRetrievalSnapshots(ctx context.Context, datasetID, profileID, status string,
	limit int) ([]model.RetrievalSnapshot, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	query := r.db.WithContext(ctx)
	if strings.TrimSpace(datasetID) != "" {
		query = query.Where("dataset_id = ?", datasetID)
	}
	if strings.TrimSpace(profileID) != "" {
		query = query.Where("retrieval_profile_id = ?", profileID)
	}
	if strings.TrimSpace(status) != "" {
		query = query.Where("status = ?", status)
	}
	var rows []retrievalSnapshotRow
	if err := query.Order("source_seq DESC, created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.RetrievalSnapshot, len(rows))
	for i := range rows {
		snapshot, err := rows[i].toModel()
		if err != nil {
			return nil, err
		}
		out[i] = *snapshot
	}
	return out, nil
}

func (r *PipelineRepo) GetLatestActiveRetrievalSnapshot(ctx context.Context, datasetID, profileID string,
	throughSeq int64) (*model.RetrievalSnapshot, error) {
	query := r.db.WithContext(ctx).Where("dataset_id = ? AND retrieval_profile_id = ? AND status = ?",
		datasetID, profileID, model.RetrievalSnapshotActive)
	if throughSeq >= 0 {
		query = query.Where("source_seq <= ?", throughSeq)
	}
	var row retrievalSnapshotRow
	result := query.Order("source_seq DESC, activated_at DESC").Limit(1).Find(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, port.ErrRetrievalSnapshotNotFound
	}
	return row.toModel()
}

func (r *PipelineRepo) SetRetrievalSnapshotStatusForStep(ctx context.Context, snapshotID, stepRunID string,
	producerAttempt int, status, failureReason string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assertActiveStepProducer(tx, stepRunID, producerAttempt); err != nil {
			return err
		}
		result := tx.Model(&retrievalSnapshotRow{}).
			Where("id = ? AND source_step_run_id = ? AND producer_attempt = ? AND status <> ?",
				snapshotID, stepRunID, producerAttempt, model.RetrievalSnapshotActive).
			Updates(map[string]any{"status": status, "failure_reason": failureReason})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("RetrievalSnapshot 状态写入被 fencing 拒绝")
		}
		return nil
	})
}

func (r *PipelineRepo) ActivateRetrievalSnapshotForStep(ctx context.Context, snapshotID, stepRunID string,
	producerAttempt int, lexicalRef, vectorRef string, lexicalCount, vectorCount int) (*model.RetrievalSnapshot, error) {
	var activated *model.RetrievalSnapshot
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assertActiveStepProducer(tx, stepRunID, producerAttempt); err != nil {
			return err
		}
		var row retrievalSnapshotRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", snapshotID).First(&row).Error; err != nil {
			return err
		}
		if row.SourceStepRunID != stepRunID {
			return fmt.Errorf("RetrievalSnapshot 不属于当前 StepRun")
		}
		if row.Status == model.RetrievalSnapshotActive {
			var err error
			activated, err = row.toModel()
			return err
		}
		if row.Status != model.RetrievalSnapshotValidating || row.ProducerAttempt != producerAttempt {
			return fmt.Errorf("RetrievalSnapshot 只能由当前 validating attempt 激活")
		}
		now := time.Now()
		if err := tx.Model(&retrievalSnapshotRow{}).Where("id = ?", snapshotID).Updates(map[string]any{
			"status": model.RetrievalSnapshotActive, "lexical_ref": lexicalRef, "vector_ref": vectorRef,
			"lexical_count": lexicalCount, "vector_count": vectorCount, "failure_reason": "", "activated_at": now,
		}).Error; err != nil {
			return err
		}
		row.Status, row.LexicalRef, row.VectorRef = model.RetrievalSnapshotActive, lexicalRef, vectorRef
		row.LexicalCount, row.VectorCount, row.FailureReason, row.ActivatedAt = lexicalCount, vectorCount, "", &now
		var err error
		activated, err = row.toModel()
		return err
	})
	return activated, err
}

func (r *PipelineRepo) UpsertRetrievalChunks(ctx context.Context, chunks []model.RetrievalChunk) error {
	if len(chunks) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := range chunks {
			chunk := &chunks[i]
			if chunk.ID == "" {
				chunk.ID = uuid.NewString()
			}
			if chunk.CreatedAt.IsZero() {
				chunk.CreatedAt = time.Now()
			}
			if len(chunk.Embedding) == 0 {
				return fmt.Errorf("RetrievalChunk %s 缺少 embedding", chunk.ID)
			}
			metadata := chunk.Metadata
			if len(metadata) == 0 {
				metadata = json.RawMessage(`{}`)
			}
			if err := tx.Exec(`INSERT INTO retrieval_chunks
				(id, dataset_id, dataset_item_id, retrieval_profile_id, chunk_no, chunk_text,
				 chunk_hash, source_seq, embedding_model, embedding, metadata, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?::jsonb, ?)
				ON CONFLICT (dataset_item_id, retrieval_profile_id, chunk_no) DO UPDATE SET
				 chunk_text = EXCLUDED.chunk_text, chunk_hash = EXCLUDED.chunk_hash,
				 source_seq = EXCLUDED.source_seq, embedding_model = EXCLUDED.embedding_model,
				 embedding = EXCLUDED.embedding, metadata = EXCLUDED.metadata`,
				chunk.ID, chunk.DatasetID, chunk.DatasetItemID, chunk.RetrievalProfileID, chunk.ChunkNo,
				chunk.ChunkText, chunk.ChunkHash, chunk.SourceSeq, chunk.EmbeddingModel,
				pgvector.NewVector(chunk.Embedding), string(metadata), chunk.CreatedAt).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *PipelineRepo) CountRetrievalChunks(ctx context.Context, datasetID, profileID string,
	sourceSeq int64) (chunkCount, itemCount int, err error) {
	var row struct {
		ChunkCount int `gorm:"column:chunk_count"`
		ItemCount  int `gorm:"column:item_count"`
	}
	err = r.db.WithContext(ctx).Raw(`SELECT COUNT(*) AS chunk_count,
		COUNT(DISTINCT dataset_item_id) AS item_count
		FROM retrieval_chunks
		WHERE dataset_id = ? AND retrieval_profile_id = ? AND source_seq <= ? AND embedding IS NOT NULL`,
		datasetID, profileID, sourceSeq).Scan(&row).Error
	return row.ChunkCount, row.ItemCount, err
}

func (r *PipelineRepo) SearchRetrievalChunks(ctx context.Context, req port.VectorSearchRequest) ([]port.RankedHit, error) {
	if len(req.QueryEmbedding) == 0 {
		return nil, fmt.Errorf("query embedding 不能为空")
	}
	limit := req.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	// 多取若干 Chunk 后按 Item 保留最高分，确保单个长 Item 不吞掉全部候选。
	chunkLimit := limit * 4
	if chunkLimit > 2000 {
		chunkLimit = 2000
	}
	sql := `SELECT rc.id AS chunk_id, rc.dataset_item_id, rc.chunk_text,
		1 - (rc.embedding <=> ?::vector) AS score
		FROM retrieval_chunks rc
		JOIN dataset_items di ON di.id = rc.dataset_item_id
		WHERE rc.dataset_id = ? AND rc.retrieval_profile_id = ?
		  AND rc.source_seq <= ? AND di.commit_seq <= ? AND rc.embedding IS NOT NULL`
	args := []any{pgvector.NewVector(req.QueryEmbedding), req.DatasetID, req.RetrievalProfileID, req.SourceSeq, req.SourceSeq}
	for field, values := range req.Filters {
		if len(values) == 0 {
			continue
		}
		sql += ` AND ((jsonb_typeof(di.fields -> ?) <> 'array' AND di.fields ->> ? IN ?)
			OR (jsonb_typeof(di.fields -> ?) = 'array' AND EXISTS (
				SELECT 1 FROM jsonb_array_elements_text(di.fields -> ?) AS value WHERE value IN ?)))`
		args = append(args, field, field, values, field, field, values)
	}
	sql += ` ORDER BY rc.embedding <=> ?::vector LIMIT ?`
	args = append(args, pgvector.NewVector(req.QueryEmbedding), chunkLimit)
	var rows []struct {
		ChunkID       string  `gorm:"column:chunk_id"`
		DatasetItemID string  `gorm:"column:dataset_item_id"`
		ChunkText     string  `gorm:"column:chunk_text"`
		Score         float64 `gorm:"column:score"`
	}
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]port.RankedHit, 0, limit)
	for _, row := range rows {
		if seen[row.DatasetItemID] {
			continue
		}
		seen[row.DatasetItemID] = true
		out = append(out, port.RankedHit{DatasetItemID: row.DatasetItemID, ChunkID: row.ChunkID,
			Text: row.ChunkText, Rank: len(out) + 1, Score: row.Score})
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (r *PipelineRepo) GetDatasetItemsByIDs(ctx context.Context, datasetID string, sourceSeq int64,
	ids []string) ([]model.DatasetItem, error) {
	if len(ids) == 0 {
		return []model.DatasetItem{}, nil
	}
	var rows []pipelineDatasetItemRow
	if err := r.db.WithContext(ctx).Where("dataset_id = ? AND commit_seq <= ? AND id IN ?", datasetID, sourceSeq, ids).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.DatasetItem, len(rows))
	for i := range rows {
		out[i] = rows[i].toModel()
	}
	return out, nil
}

func (r *PipelineRepo) AppendKnowledgeToolAudit(ctx context.Context, audit port.KnowledgeToolAudit) error {
	if audit.ID == "" {
		audit.ID = uuid.NewString()
	}
	request := audit.Request
	if len(request) == 0 {
		request = json.RawMessage(`{}`)
	}
	return r.db.WithContext(ctx).Exec(`INSERT INTO knowledge_tool_audits
		(id, scope_id, workspace_id, tool_name, source_name, request, result_count, latency_ms, error_message)
		VALUES (?, ?, ?, ?, ?, ?::jsonb, ?, ?, ?)`, audit.ID, audit.ScopeID, audit.WorkspaceID,
		audit.ToolName, audit.SourceName, string(request), audit.ResultCount, audit.LatencyMS, audit.ErrorMessage).Error
}

type retrievalProfileRow struct {
	ID              string    `gorm:"column:id;primaryKey"`
	WorkspaceID     string    `gorm:"column:workspace_id"`
	Name            string    `gorm:"column:name"`
	DatasetSchemaID string    `gorm:"column:dataset_schema_id"`
	LexicalConfig   string    `gorm:"column:lexical_config"`
	VectorConfig    string    `gorm:"column:vector_config"`
	FilterFields    string    `gorm:"column:filter_fields"`
	FusionConfig    string    `gorm:"column:fusion_config"`
	ProfileHash     string    `gorm:"column:profile_hash"`
	CreatedAt       time.Time `gorm:"column:created_at"`
}

func (retrievalProfileRow) TableName() string { return "retrieval_profiles" }

func (row retrievalProfileRow) toModel() (*model.RetrievalProfile, error) {
	profile := &model.RetrievalProfile{ID: row.ID, WorkspaceID: row.WorkspaceID, Name: row.Name,
		DatasetSchemaID: row.DatasetSchemaID, ProfileHash: row.ProfileHash, CreatedAt: row.CreatedAt}
	if err := json.Unmarshal([]byte(row.LexicalConfig), &profile.Lexical); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(row.VectorConfig), &profile.Vector); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(row.FilterFields), &profile.FilterFields); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(row.FusionConfig), &profile.Fusion); err != nil {
		return nil, err
	}
	return profile, nil
}

type retrievalSnapshotRow struct {
	ID                 string     `gorm:"column:id;primaryKey"`
	DatasetID          string     `gorm:"column:dataset_id"`
	RetrievalProfileID string     `gorm:"column:retrieval_profile_id"`
	SourceStepRunID    string     `gorm:"column:source_step_run_id"`
	ProducerAttempt    int        `gorm:"column:producer_attempt"`
	SourceSeq          int64      `gorm:"column:source_seq"`
	Status             string     `gorm:"column:status"`
	LexicalRef         string     `gorm:"column:lexical_ref"`
	VectorRef          string     `gorm:"column:vector_ref"`
	LexicalCount       int        `gorm:"column:lexical_count"`
	VectorCount        int        `gorm:"column:vector_count"`
	FailureReason      string     `gorm:"column:failure_reason"`
	CreatedAt          time.Time  `gorm:"column:created_at"`
	ActivatedAt        *time.Time `gorm:"column:activated_at"`
}

func (retrievalSnapshotRow) TableName() string { return "retrieval_snapshots" }

func (row retrievalSnapshotRow) toModel() (*model.RetrievalSnapshot, error) {
	snapshot := &model.RetrievalSnapshot{ID: row.ID, DatasetID: row.DatasetID,
		RetrievalProfileID: row.RetrievalProfileID, SourceStepRunID: row.SourceStepRunID,
		SourceSeq: row.SourceSeq, Status: row.Status, LexicalRef: row.LexicalRef,
		VectorRef: row.VectorRef, LexicalCount: row.LexicalCount, VectorCount: row.VectorCount,
		FailureReason: row.FailureReason, CreatedAt: row.CreatedAt}
	if row.ActivatedAt != nil {
		snapshot.ActivatedAt = *row.ActivatedAt
	}
	return snapshot, nil
}
