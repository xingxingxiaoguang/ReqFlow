package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type WorkItemRepo struct{ db *gorm.DB }

func NewWorkItemRepo(db *gorm.DB) *WorkItemRepo { return &WorkItemRepo{db: db} }

func (r *WorkItemRepo) UpsertWithVectors(ctx context.Context, items []port.WorkItemVector) error {
	if len(items) == 0 {
		return nil
	}
	rows := make([]workItemRow, len(items))
	for i, it := range items {
		row := workItemRow{
			ID:              it.ID,
			ProjectID:       it.ProjectID,
			Identifier:      it.Identifier,
			Title:           it.Title,
			Description:     it.Description,
			Kind:            it.Kind,
			TypeID:          it.TypeID,
			StateID:         it.StateID,
			RemoteUpdatedAt: strPtr(it.RemoteUpdatedAt),
			IsArchived:      false,
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
		DoUpdates: clause.AssignmentColumns([]string{"project_id", "identifier", "title", "description", "kind", "type_id", "state_id", "remote_updated_at", "is_archived", "embedding", "synced_at"}),
	}).Create(&rows).Error
}

func (r *WorkItemRepo) ListAll(ctx context.Context) ([]model.WorkItem, error) {
	var rows []workItemRow
	if err := r.db.WithContext(ctx).Order("synced_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return workItemRowsToModel(rows), nil
}

func (r *WorkItemRepo) ListActive(ctx context.Context, f port.WorkItemFilter) ([]model.WorkItem, int64, error) {
	q := r.db.WithContext(ctx).Model(&workItemRow{}).Where("NOT is_archived")
	if f.ProjectID != "" {
		q = q.Where("project_id = ?", f.ProjectID)
	}
	if f.Search != "" {
		pat := "%" + f.Search + "%"
		q = q.Where("(title ILIKE ? OR identifier ILIKE ?)", pat, pat)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	limit, offset := f.Limit, f.Offset
	// 上限放宽到 1 万：查重的精确匹配层需要全量拉取单项目条目
	if limit <= 0 || limit > 10000 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var rows []workItemRow
	if err := q.Order("synced_at DESC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return workItemRowsToModel(rows), total, nil
}

func (r *WorkItemRepo) Archive(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&workItemRow{}).
		Where("id IN ?", ids).Update("is_archived", true).Error
}

func (r *WorkItemRepo) CountActive(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&workItemRow{}).Where("NOT is_archived").Count(&n).Error
	return n, err
}

func (r *WorkItemRepo) SearchSimilar(ctx context.Context, vec []float32, projectID string, n int) ([]port.SimilarWorkItem, error) {
	if len(vec) == 0 || n <= 0 {
		return nil, nil
	}
	q := pgvector.NewVector(vec)
	where := "NOT is_archived AND embedding IS NOT NULL"
	args := []any{q}
	if projectID != "" {
		where += " AND project_id = ?"
		args = append(args, projectID)
	}
	args = append(args, q, n) // ORDER BY + LIMIT

	var hits []struct {
		workItemRow
		Distance float64 `gorm:"column:distance"`
	}
	err := r.db.WithContext(ctx).Raw(fmt.Sprintf(`
		SELECT id, project_id, identifier, title, description, kind, type_id, state_id,
		       remote_updated_at, is_archived, (embedding <=> ?) AS distance
		FROM work_items
		WHERE %s
		ORDER BY embedding <=> ?
		LIMIT ?`, where), args...).Scan(&hits).Error
	if err != nil {
		return nil, err
	}
	out := make([]port.SimilarWorkItem, len(hits))
	for i, h := range hits {
		out[i] = port.SimilarWorkItem{WorkItem: workItemRowsToModel([]workItemRow{h.workItemRow})[0], Distance: h.Distance}
	}
	return out, nil
}

func workItemRowsToModel(rows []workItemRow) []model.WorkItem {
	out := make([]model.WorkItem, len(rows))
	for i, row := range rows {
		out[i] = model.WorkItem{
			ID:              row.ID,
			ProjectID:       row.ProjectID,
			Identifier:      row.Identifier,
			Title:           row.Title,
			Description:     row.Description,
			Kind:            row.Kind,
			TypeID:          row.TypeID,
			StateID:         row.StateID,
			RemoteUpdatedAt: strVal(row.RemoteUpdatedAt),
			IsArchived:      row.IsArchived,
		}
	}
	return out
}
