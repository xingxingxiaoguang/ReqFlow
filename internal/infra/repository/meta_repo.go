package repository

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"reqflow/internal/domain/model"
)

type MetaRepo struct{ db *gorm.DB }

func NewMetaRepo(db *gorm.DB) *MetaRepo { return &MetaRepo{db: db} }

func (r *MetaRepo) UpsertTypes(ctx context.Context, types []model.MetaType) error {
	if len(types) == 0 {
		return nil
	}
	rows := make([]metaTypeRow, len(types))
	for i, t := range types {
		rows[i] = metaTypeRow{ID: t.ID, ProjectID: t.ProjectID, Name: t.Name, Group: t.Group}
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}, {Name: "project_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"name", `"group"`}),
	}).Create(&rows).Error
}

func (r *MetaRepo) UpsertStates(ctx context.Context, states []model.MetaState) error {
	if len(states) == 0 {
		return nil
	}
	rows := make([]metaStateRow, len(states))
	for i, s := range states {
		rows[i] = metaStateRow{
			ID: s.ID, ProjectID: s.ProjectID, WorkItemTypeID: s.WorkItemTypeID,
			Name: s.Name, Type: s.Type, Color: s.Color,
		}
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}, {Name: "project_id"}, {Name: "work_item_type_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"name", "type", "color"}),
	}).Create(&rows).Error
}

func (r *MetaRepo) UpsertPriorities(ctx context.Context, priorities []model.MetaPriority) error {
	if len(priorities) == 0 {
		return nil
	}
	rows := make([]metaPriorityRow, len(priorities))
	for i, p := range priorities {
		rows[i] = metaPriorityRow{ID: p.ID, ProjectID: p.ProjectID, Name: p.Name}
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}, {Name: "project_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"name"}),
	}).Create(&rows).Error
}

func (r *MetaRepo) ListTypes(ctx context.Context, projectID string) ([]model.MetaType, error) {
	var rows []metaTypeRow
	if err := r.db.WithContext(ctx).Where("project_id = ?", projectID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.MetaType, len(rows))
	for i, row := range rows {
		out[i] = model.MetaType{ID: row.ID, ProjectID: row.ProjectID, Name: row.Name, Group: row.Group}
	}
	return out, nil
}

func (r *MetaRepo) ListStates(ctx context.Context, projectID string) ([]model.MetaState, error) {
	var rows []metaStateRow
	if err := r.db.WithContext(ctx).Where("project_id = ?", projectID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.MetaState, len(rows))
	for i, row := range rows {
		out[i] = model.MetaState{
			ID: row.ID, ProjectID: row.ProjectID, WorkItemTypeID: row.WorkItemTypeID,
			Name: row.Name, Type: row.Type, Color: row.Color,
		}
	}
	return out, nil
}

func (r *MetaRepo) ListPriorities(ctx context.Context, projectID string) ([]model.MetaPriority, error) {
	var rows []metaPriorityRow
	if err := r.db.WithContext(ctx).Where("project_id = ?", projectID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.MetaPriority, len(rows))
	for i, row := range rows {
		out[i] = model.MetaPriority{ID: row.ID, ProjectID: row.ProjectID, Name: row.Name}
	}
	return out, nil
}
