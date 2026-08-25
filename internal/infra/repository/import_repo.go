package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"reqflow/internal/domain/model"
)

type ImportRepo struct{ db *gorm.DB }

func NewImportRepo(db *gorm.DB) *ImportRepo { return &ImportRepo{db: db} }

func (r *ImportRepo) CreateRecord(ctx context.Context, rec *model.ImportRecord) error {
	if rec.ID == "" {
		rec.ID = uuid.NewString()
	}
	now := time.Now()
	rec.CreatedAt, rec.UpdatedAt = now, now
	row := recordToRow(rec)
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return err
	}
	rec.CreatedAt, rec.UpdatedAt = row.CreatedAt, row.UpdatedAt
	return nil
}

func (r *ImportRepo) UpdateRecord(ctx context.Context, rec *model.ImportRecord) error {
	rec.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Model(&importRecordRow{}).
		Where("id = ?", rec.ID).
		Updates(map[string]any{
			"status":              rec.Status,
			"items_count":         rec.ItemsCount,
			"target_project_id":   strPtr(rec.TargetProjectID),
			"target_project_name": strPtr(rec.TargetProjectName),
			"imported_count":      rec.ImportedCount,
			"failed_count":        rec.FailedCount,
			"error_message":       strPtr(rec.ErrorMessage),
			"updated_at":          rec.UpdatedAt,
		}).Error
}

func (r *ImportRepo) ListRecords(ctx context.Context, limit int) ([]model.ImportRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []importRecordRow
	if err := r.db.WithContext(ctx).Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.ImportRecord, len(rows))
	for i, row := range rows {
		out[i] = recordToModel(&row)
	}
	return out, nil
}

func (r *ImportRepo) GetRecord(ctx context.Context, id string) (*model.ImportRecord, error) {
	var row importRecordRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	rec := recordToModel(&row)
	return &rec, nil
}

func (r *ImportRepo) GetRecordItems(ctx context.Context, recordID string) ([]model.ImportRecordItem, error) {
	var rows []importRecordItemRow
	if err := r.db.WithContext(ctx).Where("record_id = ?", recordID).Order("created_at ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.ImportRecordItem, len(rows))
	for i, row := range rows {
		out[i] = itemRowToModel(&row)
	}
	return out, nil
}

func (r *ImportRepo) ReplaceRecordItems(ctx context.Context, recordID string, items []model.ImportRecordItem) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("record_id = ?", recordID).Delete(&importRecordItemRow{}).Error; err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		rows := make([]importRecordItemRow, len(items))
		for i, it := range items {
			id := it.ID
			if id == "" {
				id = uuid.NewString()
			}
			items[i].ID = id
			rows[i] = itemToRow(id, recordID, it.DraftItem, it.Status, it.ErrorMessage)
		}
		return tx.Create(&rows).Error
	})
}

func (r *ImportRepo) UpdateItemResult(ctx context.Context, itemID, pingcodeID, identifier, status, errMsg string) error {
	return r.db.WithContext(ctx).Model(&importRecordItemRow{}).
		Where("id = ?", itemID).
		Updates(map[string]any{
			"pingcode_id":          strPtr(pingcodeID),
			"pingcode_identifier":  strPtr(identifier),
			"status":               status,
			"error_message":        strPtr(errMsg),
		}).Error
}

/* ---- 行 <-> 模型转换 ---- */

func recordToRow(rec *model.ImportRecord) *importRecordRow {
	return &importRecordRow{
		ID: rec.ID, FileName: rec.FileName, OriginalFilePath: strPtr(rec.OriginalFilePath),
		Status: rec.Status, ItemsCount: rec.ItemsCount,
		TargetProjectID: strPtr(rec.TargetProjectID), TargetProjectName: strPtr(rec.TargetProjectName),
		ImportedCount: rec.ImportedCount, FailedCount: rec.FailedCount,
		ErrorMessage: strPtr(rec.ErrorMessage),
		CreatedAt:    rec.CreatedAt, UpdatedAt: rec.UpdatedAt,
	}
}

func recordToModel(row *importRecordRow) model.ImportRecord {
	return model.ImportRecord{
		ID: row.ID, FileName: row.FileName, OriginalFilePath: strVal(row.OriginalFilePath),
		Status: row.Status, ItemsCount: row.ItemsCount,
		TargetProjectID: strVal(row.TargetProjectID), TargetProjectName: strVal(row.TargetProjectName),
		ImportedCount: row.ImportedCount, FailedCount: row.FailedCount,
		ErrorMessage: strVal(row.ErrorMessage),
		CreatedAt:    row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func itemToRow(id, recordID string, d model.DraftItem, status, errMsg string) importRecordItemRow {
	row := importRecordItemRow{
		ID: id, RecordID: recordID,
		Title: d.Title, Description: d.Description, ProjectName: d.ProjectName,
		TypeID: d.TypeID, Priority: d.Priority,
		AssigneeName:       strPtr(d.AssigneeName),
		SolutionSuggestion: d.SolutionSuggestion,
		Status:             status, ErrorMessage: strPtr(errMsg),
	}
	if d.EstimatedHours != 0 {
		h := d.EstimatedHours
		row.EstimatedHours = &h
	}
	if d.StartAt != "" {
		s := d.StartAt
		row.StartAt = &s
	}
	if d.EndAt != "" {
		e := d.EndAt
		row.EndAt = &e
	}
	return row
}

func itemRowToModel(row *importRecordItemRow) model.ImportRecordItem {
	d := model.DraftItem{
		ProjectName: row.ProjectName, Title: row.Title, Description: row.Description,
		Priority: row.Priority,
		StartAt:  strVal(row.StartAt), EndAt: strVal(row.EndAt),
		TypeID: row.TypeID, AssigneeName: strVal(row.AssigneeName),
		SolutionSuggestion: row.SolutionSuggestion,
	}
	if row.EstimatedHours != nil {
		d.EstimatedHours = *row.EstimatedHours
	}
	return model.ImportRecordItem{
		ID: row.ID, RecordID: row.RecordID, DraftItem: d,
		PingCodeID: strVal(row.PingCodeID), PingCodeIdentifier: strVal(row.PingCodeIdentifier),
		Status: row.Status, ErrorMessage: strVal(row.ErrorMessage),
	}
}
