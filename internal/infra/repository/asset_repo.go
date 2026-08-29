package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

func (r *PipelineRepo) CreateOrGetAsset(ctx context.Context, asset *model.Asset) (*model.Asset, bool, error) {
	if asset.ID == "" {
		asset.ID = uuid.NewString()
	}
	if asset.CreatedAt.IsZero() {
		asset.CreatedAt = time.Now()
	}
	result := r.db.WithContext(ctx).Exec(`INSERT INTO assets
		(id, workspace_id, filename, mime_type, size_bytes, sha256, blob_uri, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (workspace_id, sha256) DO NOTHING`,
		asset.ID, asset.WorkspaceID, asset.Filename, asset.MIMEType, asset.SizeBytes,
		asset.SHA256, asset.BlobURI, asset.CreatedAt)
	if result.Error != nil {
		return nil, false, result.Error
	}
	stored, err := r.getAssetByDigest(ctx, asset.WorkspaceID, asset.SHA256)
	return stored, result.RowsAffected == 1, err
}

func (r *PipelineRepo) GetAsset(ctx context.Context, id string) (*model.Asset, error) {
	var row assetV2Row
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	asset := row.toModel()
	return &asset, nil
}

func (r *PipelineRepo) getAssetByDigest(ctx context.Context, workspaceID, digest string) (*model.Asset, error) {
	var row assetV2Row
	if err := r.db.WithContext(ctx).Where("workspace_id = ? AND sha256 = ?", workspaceID, digest).First(&row).Error; err != nil {
		return nil, err
	}
	asset := row.toModel()
	return &asset, nil
}

func (r *PipelineRepo) CreateAssetSet(ctx context.Context, set *model.AssetSet, members []model.AssetSetMember) error {
	if set.ID == "" {
		set.ID = uuid.NewString()
	}
	if set.CreatedAt.IsZero() {
		set.CreatedAt = time.Now()
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO asset_sets (id, workspace_id, name, created_by, created_at)
			VALUES (?, ?, ?, ?, ?)`, set.ID, set.WorkspaceID, set.Name, set.CreatedBy, set.CreatedAt).Error; err != nil {
			return err
		}
		for i := range members {
			members[i].AssetSetID = set.ID
			result := tx.Exec(`INSERT INTO asset_set_members (asset_set_id, asset_id, ordinal)
				SELECT ?, id, ? FROM assets WHERE id = ? AND workspace_id = ?`,
				set.ID, members[i].Ordinal, members[i].AssetID, set.WorkspaceID)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("Asset %s 不存在或不属于 workspace %s", members[i].AssetID, set.WorkspaceID)
			}
		}
		return nil
	})
}

func (r *PipelineRepo) GetAssetSet(ctx context.Context, id string) (*model.AssetSet, error) {
	var row assetSetV2Row
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	set := row.toModel()
	return &set, nil
}

func (r *PipelineRepo) ListAssetSetEntries(ctx context.Context, assetSetID string) ([]model.AssetSetEntry, error) {
	var rows []assetSetEntryRow
	err := r.db.WithContext(ctx).Table("asset_set_members AS m").
		Select(`m.asset_set_id, m.asset_id, m.ordinal, a.workspace_id, a.filename,
			a.mime_type, a.size_bytes, a.sha256, a.blob_uri, a.created_at`).
		Joins("JOIN assets AS a ON a.id = m.asset_id").Where("m.asset_set_id = ?", assetSetID).
		Order("m.ordinal ASC").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	entries := make([]model.AssetSetEntry, len(rows))
	for i := range rows {
		entries[i] = rows[i].toModel()
	}
	return entries, nil
}

func (r *PipelineRepo) GetParsedDocument(ctx context.Context, assetID, parserName, parserVersion string) (*model.ParsedDocument, []model.DocumentBlock, error) {
	var row parsedDocumentV2Row
	if err := r.db.WithContext(ctx).Where("asset_id = ? AND parser_name = ? AND parser_version = ?",
		assetID, parserName, parserVersion).Limit(1).Find(&row).Error; err != nil {
		return nil, nil, err
	}
	if row.ID == "" {
		return nil, nil, port.ErrResourceNotFound
	}
	var blockRows []documentBlockV2Row
	if err := r.db.WithContext(ctx).Where("parsed_document_id = ?", row.ID).
		Order("ordinal ASC").Find(&blockRows).Error; err != nil {
		return nil, nil, err
	}
	blocks := make([]model.DocumentBlock, len(blockRows))
	for i := range blockRows {
		blocks[i] = blockRows[i].toModel()
	}
	document := row.toModel()
	return &document, blocks, nil
}

func (r *PipelineRepo) GetParsedDocumentByID(ctx context.Context, id string) (*model.ParsedDocument, error) {
	var row parsedDocumentV2Row
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	document := row.toModel()
	return &document, nil
}

func (r *PipelineRepo) ListDocumentBlocks(ctx context.Context, parsedDocumentID string, afterOrdinal, limit int) ([]model.DocumentBlock, error) {
	if afterOrdinal < -1 {
		afterOrdinal = -1
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var rows []documentBlockV2Row
	if err := r.db.WithContext(ctx).Where("parsed_document_id = ? AND ordinal > ?", parsedDocumentID, afterOrdinal).
		Order("ordinal ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	blocks := make([]model.DocumentBlock, len(rows))
	for i := range rows {
		blocks[i] = rows[i].toModel()
	}
	return blocks, nil
}

func (r *PipelineRepo) SaveParsedDocument(ctx context.Context, document *model.ParsedDocument, blocks []model.DocumentBlock) (*model.ParsedDocument, error) {
	if document.Status != model.ParsedDocumentSucceeded && document.Status != model.ParsedDocumentFailed {
		return nil, fmt.Errorf("ParsedDocument 终态非法: %s", document.Status)
	}
	var stored model.ParsedDocument
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if document.ID == "" {
			document.ID = uuid.NewString()
		}
		if document.CreatedAt.IsZero() {
			document.CreatedAt = time.Now()
		}
		if err := tx.Exec(`INSERT INTO parsed_documents
			(id, asset_id, parser_name, parser_version, status, content_hash, error_message, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (asset_id, parser_name, parser_version) DO NOTHING`,
			document.ID, document.AssetID, document.ParserName, document.ParserVersion,
			document.Status, document.ContentHash, document.ErrorMessage, document.CreatedAt).Error; err != nil {
			return err
		}
		var row parsedDocumentV2Row
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("asset_id = ? AND parser_name = ? AND parser_version = ?", document.AssetID,
				document.ParserName, document.ParserVersion).First(&row).Error; err != nil {
			return err
		}
		// 成功缓存永不被并发的失败结果降级。
		if row.Status == model.ParsedDocumentSucceeded && document.Status == model.ParsedDocumentFailed {
			stored = row.toModel()
			return nil
		}
		if err := tx.Model(&parsedDocumentV2Row{}).Where("id = ?", row.ID).Updates(map[string]any{
			"status": document.Status, "content_hash": document.ContentHash,
			"error_message": document.ErrorMessage,
		}).Error; err != nil {
			return err
		}
		if document.Status == model.ParsedDocumentSucceeded {
			if err := tx.Where("parsed_document_id = ?", row.ID).Delete(&documentBlockV2Row{}).Error; err != nil {
				return err
			}
			for i := range blocks {
				item := &blocks[i]
				if item.ID == "" {
					item.ID = uuid.NewString()
				}
				item.ParsedDocumentID = row.ID
				if !json.Valid([]byte(item.Metadata)) {
					return fmt.Errorf("DocumentBlock %d metadata 不是合法 JSON", i)
				}
				if err := tx.Exec(`INSERT INTO document_blocks
					(id, parsed_document_id, ordinal, block_type, page_no, section_path, text, metadata)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?::jsonb)`, item.ID, row.ID, item.Ordinal,
					item.BlockType, item.PageNo, item.SectionPath, item.Text, item.Metadata).Error; err != nil {
					return err
				}
			}
		}
		row.Status, row.ContentHash, row.ErrorMessage = document.Status, document.ContentHash, document.ErrorMessage
		stored = row.toModel()
		return nil
	})
	return &stored, err
}

func (r *PipelineRepo) BeginParsedDocumentSet(ctx context.Context, set *model.ParsedDocumentSet, members []model.ParsedDocumentSetItem) (*model.ParsedDocumentSet, error) {
	var stored model.ParsedDocumentSet
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var producer struct {
			Attempt    int
			Status     string
			LeaseUntil *time.Time
		}
		if err := tx.Raw(`SELECT attempt, status, lease_until FROM step_runs
			WHERE id = ? FOR UPDATE`, set.SourceStepRunID).Scan(&producer).Error; err != nil {
			return err
		}
		if producer.Attempt != set.ProducerAttempt || producer.Status != model.StepRunRunning ||
			producer.LeaseUntil == nil || !producer.LeaseUntil.After(time.Now()) {
			return port.ErrStaleResourceExecution
		}
		var row parsedDocumentSetV2Row
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("source_step_run_id = ?", set.SourceStepRunID).Limit(1).Find(&row).Error
		switch {
		case err == nil && row.ID == "":
			if set.ID == "" {
				set.ID = uuid.NewString()
			}
			if set.CreatedAt.IsZero() {
				set.CreatedAt = time.Now()
			}
			if err := tx.Exec(`INSERT INTO parsed_document_sets
				(id, asset_set_id, source_step_run_id, parser_name, parser_version, status,
				 producer_attempt, total_count, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, set.ID, set.AssetSetID, set.SourceStepRunID,
				set.ParserName, set.ParserVersion, model.ParsedDocumentSetRunning,
				set.ProducerAttempt, len(members), set.CreatedAt).Error; err != nil {
				return err
			}
			row = parsedDocumentSetV2Row{ID: set.ID, AssetSetID: set.AssetSetID,
				SourceStepRunID: set.SourceStepRunID, ParserName: set.ParserName,
				ParserVersion: set.ParserVersion, Status: model.ParsedDocumentSetRunning,
				ProducerAttempt: set.ProducerAttempt, TotalCount: len(members), CreatedAt: set.CreatedAt}
		case err != nil:
			return err
		default:
			if row.ProducerAttempt > set.ProducerAttempt {
				return port.ErrStaleResourceExecution
			}
			if row.AssetSetID != set.AssetSetID || row.ParserName != set.ParserName || row.ParserVersion != set.ParserVersion {
				return fmt.Errorf("StepRun %s 的解析输入或 Parser 身份发生变化", set.SourceStepRunID)
			}
			if row.ProducerAttempt < set.ProducerAttempt {
				if err := tx.Model(&parsedDocumentSetV2Row{}).Where("id = ?", row.ID).Updates(map[string]any{
					"producer_attempt": set.ProducerAttempt, "status": model.ParsedDocumentSetRunning,
					"succeeded_count": 0, "failed_count": 0, "finished_at": nil,
				}).Error; err != nil {
					return err
				}
				if err := tx.Exec(`UPDATE parsed_document_set_items SET status = ?, parsed_document_id = NULL,
					error_message = '' WHERE parsed_document_set_id = ? AND status <> ?`,
					model.ParsedDocumentPending, row.ID, model.ParsedDocumentSucceeded).Error; err != nil {
					return err
				}
				row.ProducerAttempt, row.Status, row.FinishedAt = set.ProducerAttempt, model.ParsedDocumentSetRunning, nil
			}
		}
		for _, item := range members {
			if err := tx.Exec(`INSERT INTO parsed_document_set_items
				(parsed_document_set_id, asset_id, ordinal, status) VALUES (?, ?, ?, ?)
				ON CONFLICT (parsed_document_set_id, asset_id) DO NOTHING`,
				row.ID, item.AssetID, item.Ordinal, model.ParsedDocumentPending).Error; err != nil {
				return err
			}
		}
		stored = row.toModel()
		return nil
	})
	return &stored, err
}

func (r *PipelineRepo) RecordParsedDocumentSetItem(ctx context.Context, setID string, producerAttempt int, item model.ParsedDocumentSetItem) error {
	result := r.db.WithContext(ctx).Exec(`UPDATE parsed_document_set_items AS i
		SET parsed_document_id = ?, status = ?, error_message = ?
		FROM parsed_document_sets AS s, step_runs AS sr
		WHERE i.parsed_document_set_id = s.id AND s.id = ? AND i.asset_id = ?
		  AND sr.id = s.source_step_run_id AND s.producer_attempt = ? AND s.status = ?
		  AND sr.attempt = ? AND sr.status = ? AND sr.lease_until > ?`, nullableUUID(item.ParsedDocumentID),
		item.Status, item.ErrorMessage, setID, item.AssetID, producerAttempt, model.ParsedDocumentSetRunning,
		producerAttempt, model.StepRunRunning, time.Now())
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return port.ErrStaleResourceExecution
	}
	return nil
}

func (r *PipelineRepo) FinalizeParsedDocumentSet(ctx context.Context, setID string, producerAttempt int) (*model.ParsedDocumentSet, error) {
	var completed model.ParsedDocumentSet
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row parsedDocumentSetV2Row
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", setID).First(&row).Error; err != nil {
			return err
		}
		if row.ProducerAttempt != producerAttempt || row.Status != model.ParsedDocumentSetRunning {
			return port.ErrStaleResourceExecution
		}
		var validProducer int64
		if err := tx.Raw(`SELECT count(*) FROM step_runs WHERE id = ? AND attempt = ?
			AND status = ? AND lease_until > ?`, row.SourceStepRunID, producerAttempt,
			model.StepRunRunning, time.Now()).Scan(&validProducer).Error; err != nil {
			return err
		}
		if validProducer != 1 {
			return port.ErrStaleResourceExecution
		}
		var counts struct {
			Total     int
			Succeeded int
			Failed    int
			Pending   int
		}
		if err := tx.Raw(`SELECT count(*) AS total,
			count(*) FILTER (WHERE status = ?) AS succeeded,
			count(*) FILTER (WHERE status = ?) AS failed,
			count(*) FILTER (WHERE status NOT IN (?, ?)) AS pending
			FROM parsed_document_set_items WHERE parsed_document_set_id = ?`,
			model.ParsedDocumentSucceeded, model.ParsedDocumentFailed,
			model.ParsedDocumentSucceeded, model.ParsedDocumentFailed, setID).Scan(&counts).Error; err != nil {
			return err
		}
		if counts.Pending != 0 || counts.Total != row.TotalCount {
			return fmt.Errorf("ParsedDocumentSet 尚有 %d 个文件未完成", counts.Pending)
		}
		status := model.ParsedDocumentSetPartial
		if counts.Failed == 0 {
			status = model.ParsedDocumentSetSucceeded
		} else if counts.Succeeded == 0 {
			status = model.ParsedDocumentSetFailed
		}
		now := time.Now()
		if err := tx.Model(&parsedDocumentSetV2Row{}).Where("id = ?", setID).Updates(map[string]any{
			"status": status, "succeeded_count": counts.Succeeded, "failed_count": counts.Failed,
			"finished_at": now,
		}).Error; err != nil {
			return err
		}
		row.Status, row.SucceededCount, row.FailedCount, row.FinishedAt = status, counts.Succeeded, counts.Failed, &now
		completed = row.toModel()
		return nil
	})
	return &completed, err
}

func (r *PipelineRepo) GetParsedDocumentSet(ctx context.Context, id string) (*model.ParsedDocumentSet, []model.ParsedDocumentSetItem, error) {
	var row parsedDocumentSetV2Row
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, nil, err
	}
	var itemRows []parsedDocumentSetItemV2Row
	if err := r.db.WithContext(ctx).Where("parsed_document_set_id = ?", id).
		Order("ordinal ASC").Find(&itemRows).Error; err != nil {
		return nil, nil, err
	}
	items := make([]model.ParsedDocumentSetItem, len(itemRows))
	for i := range itemRows {
		items[i] = itemRows[i].toModel()
	}
	set := row.toModel()
	return &set, items, nil
}

type assetV2Row struct {
	ID          string    `gorm:"column:id;primaryKey"`
	WorkspaceID string    `gorm:"column:workspace_id"`
	Filename    string    `gorm:"column:filename"`
	MIMEType    string    `gorm:"column:mime_type"`
	SizeBytes   int64     `gorm:"column:size_bytes"`
	SHA256      string    `gorm:"column:sha256"`
	BlobURI     string    `gorm:"column:blob_uri"`
	CreatedAt   time.Time `gorm:"column:created_at"`
}

func (assetV2Row) TableName() string { return "assets" }
func (row assetV2Row) toModel() model.Asset {
	return model.Asset{ID: row.ID, WorkspaceID: row.WorkspaceID, Filename: row.Filename,
		MIMEType: row.MIMEType, SizeBytes: row.SizeBytes, SHA256: row.SHA256,
		BlobURI: row.BlobURI, CreatedAt: row.CreatedAt}
}

type assetSetV2Row struct {
	ID          string    `gorm:"column:id;primaryKey"`
	WorkspaceID string    `gorm:"column:workspace_id"`
	Name        string    `gorm:"column:name"`
	CreatedBy   string    `gorm:"column:created_by"`
	CreatedAt   time.Time `gorm:"column:created_at"`
}

func (assetSetV2Row) TableName() string { return "asset_sets" }
func (row assetSetV2Row) toModel() model.AssetSet {
	return model.AssetSet{ID: row.ID, WorkspaceID: row.WorkspaceID, Name: row.Name,
		CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt}
}

type assetSetEntryRow struct {
	AssetSetID  string    `gorm:"column:asset_set_id"`
	AssetID     string    `gorm:"column:asset_id"`
	Ordinal     int       `gorm:"column:ordinal"`
	WorkspaceID string    `gorm:"column:workspace_id"`
	Filename    string    `gorm:"column:filename"`
	MIMEType    string    `gorm:"column:mime_type"`
	SizeBytes   int64     `gorm:"column:size_bytes"`
	SHA256      string    `gorm:"column:sha256"`
	BlobURI     string    `gorm:"column:blob_uri"`
	CreatedAt   time.Time `gorm:"column:created_at"`
}

func (row assetSetEntryRow) toModel() model.AssetSetEntry {
	asset := model.Asset{ID: row.AssetID, WorkspaceID: row.WorkspaceID, Filename: row.Filename,
		MIMEType: row.MIMEType, SizeBytes: row.SizeBytes, SHA256: row.SHA256,
		BlobURI: row.BlobURI, CreatedAt: row.CreatedAt}
	return model.AssetSetEntry{Member: model.AssetSetMember{AssetSetID: row.AssetSetID,
		AssetID: row.AssetID, Ordinal: row.Ordinal}, Asset: asset}
}

type parsedDocumentV2Row struct {
	ID            string    `gorm:"column:id;primaryKey"`
	AssetID       string    `gorm:"column:asset_id"`
	ParserName    string    `gorm:"column:parser_name"`
	ParserVersion string    `gorm:"column:parser_version"`
	Status        string    `gorm:"column:status"`
	ContentHash   string    `gorm:"column:content_hash"`
	ErrorMessage  string    `gorm:"column:error_message"`
	CreatedAt     time.Time `gorm:"column:created_at"`
}

func (parsedDocumentV2Row) TableName() string { return "parsed_documents" }
func (row parsedDocumentV2Row) toModel() model.ParsedDocument {
	return model.ParsedDocument{ID: row.ID, AssetID: row.AssetID, ParserName: row.ParserName,
		ParserVersion: row.ParserVersion, Status: row.Status, ContentHash: row.ContentHash,
		ErrorMessage: row.ErrorMessage, CreatedAt: row.CreatedAt}
}

type documentBlockV2Row struct {
	ID               string `gorm:"column:id;primaryKey"`
	ParsedDocumentID string `gorm:"column:parsed_document_id"`
	Ordinal          int    `gorm:"column:ordinal"`
	BlockType        string `gorm:"column:block_type"`
	PageNo           int    `gorm:"column:page_no"`
	SectionPath      string `gorm:"column:section_path"`
	Text             string `gorm:"column:text"`
	Metadata         string `gorm:"column:metadata;type:jsonb"`
}

func (documentBlockV2Row) TableName() string { return "document_blocks" }
func (row documentBlockV2Row) toModel() model.DocumentBlock {
	return model.DocumentBlock{ID: row.ID, ParsedDocumentID: row.ParsedDocumentID,
		Ordinal: row.Ordinal, BlockType: row.BlockType, PageNo: row.PageNo,
		SectionPath: row.SectionPath, Text: row.Text, Metadata: row.Metadata}
}

type parsedDocumentSetV2Row struct {
	ID              string     `gorm:"column:id;primaryKey"`
	AssetSetID      string     `gorm:"column:asset_set_id"`
	SourceStepRunID string     `gorm:"column:source_step_run_id"`
	ParserName      string     `gorm:"column:parser_name"`
	ParserVersion   string     `gorm:"column:parser_version"`
	Status          string     `gorm:"column:status"`
	ProducerAttempt int        `gorm:"column:producer_attempt"`
	TotalCount      int        `gorm:"column:total_count"`
	SucceededCount  int        `gorm:"column:succeeded_count"`
	FailedCount     int        `gorm:"column:failed_count"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	FinishedAt      *time.Time `gorm:"column:finished_at"`
}

func (parsedDocumentSetV2Row) TableName() string { return "parsed_document_sets" }
func (row parsedDocumentSetV2Row) toModel() model.ParsedDocumentSet {
	set := model.ParsedDocumentSet{ID: row.ID, AssetSetID: row.AssetSetID,
		SourceStepRunID: row.SourceStepRunID, ParserName: row.ParserName,
		ParserVersion: row.ParserVersion, Status: row.Status, ProducerAttempt: row.ProducerAttempt,
		TotalCount: row.TotalCount, SucceededCount: row.SucceededCount,
		FailedCount: row.FailedCount, CreatedAt: row.CreatedAt}
	if row.FinishedAt != nil {
		set.FinishedAt = *row.FinishedAt
	}
	return set
}

type parsedDocumentSetItemV2Row struct {
	ParsedDocumentSetID string  `gorm:"column:parsed_document_set_id"`
	AssetID             string  `gorm:"column:asset_id"`
	ParsedDocumentID    *string `gorm:"column:parsed_document_id"`
	Ordinal             int     `gorm:"column:ordinal"`
	Status              string  `gorm:"column:status"`
	ErrorMessage        string  `gorm:"column:error_message"`
}

func (parsedDocumentSetItemV2Row) TableName() string { return "parsed_document_set_items" }
func (row parsedDocumentSetItemV2Row) toModel() model.ParsedDocumentSetItem {
	return model.ParsedDocumentSetItem{ParsedDocumentSetID: row.ParsedDocumentSetID,
		AssetID: row.AssetID, ParsedDocumentID: strVal(row.ParsedDocumentID), Ordinal: row.Ordinal,
		Status: row.Status, ErrorMessage: row.ErrorMessage}
}
