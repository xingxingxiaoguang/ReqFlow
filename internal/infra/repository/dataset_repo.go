package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type DatasetRepo struct{ db *gorm.DB }

func NewDatasetRepo(db *gorm.DB) *DatasetRepo { return &DatasetRepo{db: db} }

func (r *DatasetRepo) CreateDataset(ctx context.Context, d *model.Dataset) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	d.CreatedAt = time.Now()
	return r.db.WithContext(ctx).Create(datasetToRow(d)).Error
}

func (r *DatasetRepo) UpdateDataset(ctx context.Context, d *model.Dataset) error {
	return r.db.WithContext(ctx).Model(&datasetRow{}).
		Where("id = ?", d.ID).
		Updates(map[string]any{
			"status":     d.Status,
			"item_count": d.ItemCount,
		}).Error
}

func (r *DatasetRepo) ListDatasets(ctx context.Context, typ string, limit int) ([]model.Dataset, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := r.db.WithContext(ctx)
	if typ != "" {
		q = q.Where("type = ?", typ)
	}
	var rows []datasetRow
	if err := q.Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.Dataset, len(rows))
	for i, row := range rows {
		out[i] = datasetToModel(&row)
	}
	return out, nil
}

func (r *DatasetRepo) GetDataset(ctx context.Context, id string) (*model.Dataset, error) {
	var row datasetRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	d := datasetToModel(&row)
	return &d, nil
}

func (r *DatasetRepo) CountDatasets(ctx context.Context, typ string) (int64, error) {
	var n int64
	q := r.db.WithContext(ctx).Model(&datasetRow{})
	if typ != "" {
		q = q.Where("type = ?", typ)
	}
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

func (r *DatasetRepo) CountDatasetItems(ctx context.Context, typ string) (int64, error) {
	var n int64
	q := r.db.WithContext(ctx).Model(&datasetItemRow{}).
		Joins("JOIN datasets ON datasets.id = dataset_items.dataset_id")
	if typ != "" {
		q = q.Where("datasets.type = ?", typ)
	}
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

func (r *DatasetRepo) ReplaceDatasetItems(ctx context.Context, datasetID string, items []port.DatasetItemVector) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("dataset_id = ?", datasetID).Delete(&datasetItemRow{}).Error; err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		rows := make([]datasetItemRow, len(items))
		for i, it := range items {
			id := it.ID
			if id == "" {
				id = uuid.NewString()
			}
			rows[i] = datasetItemRow{
				ID: id, DatasetID: datasetID, Fields: it.Fields,
				Embedding: vectorPtr(it.Embedding), CreatedAt: time.Now(),
			}
		}
		return tx.Create(&rows).Error
	})
}

func (r *DatasetRepo) ListDatasetItemsByType(ctx context.Context, typ string) ([]model.DatasetItem, error) {
	var rows []datasetItemRow
	if err := r.db.WithContext(ctx).
		Joins("JOIN datasets ON datasets.id = dataset_items.dataset_id").
		Where("datasets.type = ?", typ).
		Order("dataset_items.created_at ASC, dataset_items.id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.DatasetItem, len(rows))
	for i, row := range rows {
		out[i] = datasetItemRowToModel(&row)
	}
	return out, nil
}

func (r *DatasetRepo) ListDatasetItems(ctx context.Context, datasetID string, limit int) ([]model.DatasetItem, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var rows []datasetItemRow
	if err := r.db.WithContext(ctx).Where("dataset_id = ?", datasetID).
		Order("created_at ASC, id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.DatasetItem, len(rows))
	for i, row := range rows {
		out[i] = datasetItemRowToModel(&row)
	}
	return out, nil
}

func (r *DatasetRepo) SearchSimilarDatasetItems(ctx context.Context, vec []float32, typ string, n int) ([]port.SimilarDatasetItem, error) {
	// 显式列映射（勿用嵌套结构体 Scan——GORM 对带 TableName 的嵌入结构映射不全，fields 会丢）
	var rows []struct {
		ID        string  `gorm:"column:id"`
		DatasetID string  `gorm:"column:dataset_id"`
		Fields    string  `gorm:"column:fields"`
		Distance  float64 `gorm:"column:distance"`
	}
	if err := r.db.WithContext(ctx).Raw(`
		SELECT di.id, di.dataset_id, di.fields, di.embedding <=> ? AS distance
		FROM dataset_items di
		JOIN datasets d ON d.id = di.dataset_id
		WHERE d.type = ? AND di.embedding IS NOT NULL
		ORDER BY distance
		LIMIT ?`, pgvector.NewVector(vec), typ, n).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]port.SimilarDatasetItem, len(rows))
	for i, row := range rows {
		out[i] = port.SimilarDatasetItem{
			DatasetItem: model.DatasetItem{ID: row.ID, DatasetID: row.DatasetID, Fields: row.Fields},
			Distance:    row.Distance,
		}
	}
	return out, nil
}

/* ---- 行 <-> 模型转换 ---- */

func datasetToRow(d *model.Dataset) *datasetRow {
	return &datasetRow{
		ID: d.ID, Type: d.Type, Name: d.Name,
		SourceTaskID: strPtr(d.SourceTaskID), Status: d.Status,
		ItemCount: d.ItemCount, CreatedAt: d.CreatedAt,
	}
}

func datasetToModel(row *datasetRow) model.Dataset {
	return model.Dataset{
		ID: row.ID, Type: row.Type, Name: row.Name,
		SourceTaskID: strVal(row.SourceTaskID), Status: row.Status,
		ItemCount: row.ItemCount, CreatedAt: row.CreatedAt,
	}
}

func datasetItemRowToModel(row *datasetItemRow) model.DatasetItem {
	return model.DatasetItem{
		ID: row.ID, DatasetID: row.DatasetID, Fields: row.Fields, CreatedAt: row.CreatedAt,
	}
}

func vectorPtr(v []float32) *pgvector.Vector {
	if len(v) == 0 {
		return nil
	}
	vec := pgvector.NewVector(v)
	return &vec
}
