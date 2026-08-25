package repository

import (
	"context"
	"time"

	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type ProjectRepo struct{ db *gorm.DB }

func NewProjectRepo(db *gorm.DB) *ProjectRepo { return &ProjectRepo{db: db} }

func (r *ProjectRepo) UpsertWithVectors(ctx context.Context, items []port.ProjectVector) error {
	if len(items) == 0 {
		return nil
	}
	rows := make([]projectRow, len(items))
	for i, it := range items {
		row := projectRow{
			ID:              it.ID,
			Name:            it.Name,
			Description:     it.Description,
			RemoteUpdatedAt: strPtr(it.RemoteUpdatedAt),
			IsArchived:      false, // 出现即恢复
			SyncedAt:        time.Now(),
		}
		if len(it.Embedding) > 0 {
			v := pgvector.NewVector(it.Embedding)
			row.Embedding = &v
		}
		rows[i] = row
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"name", "description", "remote_updated_at", "is_archived", "embedding", "synced_at"}),
	}).Create(&rows).Error
}

func (r *ProjectRepo) ListAll(ctx context.Context) ([]model.Project, error) {
	var rows []projectRow
	if err := r.db.WithContext(ctx).Order("name").Find(&rows).Error; err != nil {
		return nil, err
	}
	return projectRowsToModel(rows), nil
}

func (r *ProjectRepo) ListActive(ctx context.Context) ([]model.Project, error) {
	var rows []projectRow
	if err := r.db.WithContext(ctx).Where("NOT is_archived").Order("name").Find(&rows).Error; err != nil {
		return nil, err
	}
	return projectRowsToModel(rows), nil
}

func (r *ProjectRepo) Archive(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&projectRow{}).
		Where("id IN ?", ids).Update("is_archived", true).Error
}

func (r *ProjectRepo) CountActive(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&projectRow{}).Where("NOT is_archived").Count(&n).Error
	return n, err
}

func (r *ProjectRepo) SearchSimilar(ctx context.Context, vec []float32, n int) ([]port.SimilarProject, error) {
	if len(vec) == 0 || n <= 0 {
		return nil, nil
	}
	q := pgvector.NewVector(vec)
	var hits []struct {
		projectRow
		Distance float64 `gorm:"column:distance"`
	}
	err := r.db.WithContext(ctx).Raw(`
		SELECT id, name, description, remote_updated_at, is_archived,
		       (embedding <=> ?) AS distance
		FROM projects
		WHERE NOT is_archived AND embedding IS NOT NULL
		ORDER BY embedding <=> ?
		LIMIT ?`, q, q, n).Scan(&hits).Error
	if err != nil {
		return nil, err
	}
	out := make([]port.SimilarProject, len(hits))
	for i, h := range hits {
		p := projectRowsToModel([]projectRow{h.projectRow})[0]
		out[i] = port.SimilarProject{Project: p, Distance: h.Distance}
	}
	return out, nil
}

func projectRowsToModel(rows []projectRow) []model.Project {
	out := make([]model.Project, len(rows))
	for i, row := range rows {
		out[i] = model.Project{
			ID:              row.ID,
			Name:            row.Name,
			Description:     row.Description,
			RemoteUpdatedAt: strVal(row.RemoteUpdatedAt),
			IsArchived:      row.IsArchived,
		}
	}
	return out
}
