package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type TaskRepo struct{ db *gorm.DB }

func NewTaskRepo(db *gorm.DB) *TaskRepo { return &TaskRepo{db: db} }

func (r *TaskRepo) CreateTask(ctx context.Context, t *model.Task) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	now := time.Now()
	t.CreatedAt, t.UpdatedAt = now, now
	row := taskToRow(t)
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return err
	}
	t.CreatedAt, t.UpdatedAt = row.CreatedAt, row.UpdatedAt
	return nil
}

func (r *TaskRepo) UpdateTask(ctx context.Context, t *model.Task) error {
	t.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Model(&taskRow{}).
		Where("id = ?", t.ID).
		Updates(map[string]any{
			"type":               t.Type,
			"title":              t.Title,
			"status":             t.Status,
			"current_step":       t.CurrentStep,
			"workflow":           strPtr(t.Workflow),
			"input":              strPtr(t.Input),
			"output":             strPtr(t.Output),
			"agent_context":      strPtr(t.AgentContext),
			"items_count":        t.ItemsCount,
			"imported_count":     t.ImportedCount,
			"failed_count":       t.FailedCount,
			"target_project_id":  strPtr(t.TargetProjectID),
			"target_project_name": strPtr(t.TargetProjectName),
			"output_dataset_id":  strPtr(t.OutputDatasetID),
			"input_dataset_id":   strPtr(t.InputDatasetID),
			"error_message":      strPtr(t.ErrorMessage),
			"started_at":         timePtr(t.StartedAt),
			"finished_at":        timePtr(t.FinishedAt),
			"updated_at":         t.UpdatedAt,
		}).Error
}

func (r *TaskRepo) ListTasks(ctx context.Context, f port.TaskFilter) ([]model.Task, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := r.db.WithContext(ctx)
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.Type != "" {
		q = q.Where("type = ?", f.Type)
	}
	var rows []taskRow
	if err := q.Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.Task, len(rows))
	for i, row := range rows {
		out[i] = taskToModel(&row)
	}
	return out, nil
}

func (r *TaskRepo) CountTasks(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&taskRow{}).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

func (r *TaskRepo) GetTask(ctx context.Context, id string) (*model.Task, error) {
	var row taskRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	t := taskToModel(&row)
	return &t, nil
}

func (r *TaskRepo) CreateTaskSteps(ctx context.Context, taskID string, steps []model.TaskStep) error {
	if len(steps) == 0 {
		return nil
	}
	rows := make([]taskStepRow, len(steps))
	for i, s := range steps {
		if s.ID == "" {
			s.ID = uuid.NewString()
		}
		rows[i] = stepToRow(s, taskID)
	}
	return r.db.WithContext(ctx).Create(&rows).Error
}

func (r *TaskRepo) GetTaskSteps(ctx context.Context, taskID string) ([]model.TaskStep, error) {
	var rows []taskStepRow
	if err := r.db.WithContext(ctx).Where("task_id = ?", taskID).Order("seq ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.TaskStep, len(rows))
	for i, row := range rows {
		out[i] = stepRowToModel(&row)
	}
	return out, nil
}

func (r *TaskRepo) UpdateTaskStep(ctx context.Context, step *model.TaskStep) error {
	return r.db.WithContext(ctx).Model(&taskStepRow{}).
		Where("id = ?", step.ID).
		Updates(map[string]any{
			"status":     step.Status,
			"detail":     step.Detail,
			"data":       strPtr(step.Data),
			"started_at": timePtr(step.StartedAt),
			"ended_at":   timePtr(step.EndedAt),
		}).Error
}

func (r *TaskRepo) GetTaskItems(ctx context.Context, taskID string) ([]model.TaskItem, error) {
	var rows []taskItemRow
	if err := r.db.WithContext(ctx).Where("task_id = ?", taskID).Order("created_at ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.TaskItem, len(rows))
	for i, row := range rows {
		out[i] = itemRowToModel(&row)
	}
	return out, nil
}

func (r *TaskRepo) ReplaceTaskItems(ctx context.Context, taskID string, items []model.TaskItem) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 只替换未入数据集的旧行（已生成的保留）
		if err := tx.Where("task_id = ? AND status = ?", taskID, model.ItemStatusPending).
			Delete(&taskItemRow{}).Error; err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		rows := make([]taskItemRow, len(items))
		for i, it := range items {
			id := it.ID
			if id == "" {
				id = uuid.NewString()
			}
			items[i].ID = id
			rows[i] = itemToRow(id, taskID, it)
		}
		return tx.Create(&rows).Error
	})
}

func (r *TaskRepo) UpdateItemResult(ctx context.Context, itemID, status, errMsg string) error {
	return r.db.WithContext(ctx).Model(&taskItemRow{}).
		Where("id = ?", itemID).
		Updates(map[string]any{
			"status":        status,
			"error_message": strPtr(errMsg),
		}).Error
}

func (r *TaskRepo) RecoverStuck(ctx context.Context) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if err := tx.Model(&taskRow{}).Where("status = ?", model.TaskStatusRunning).
			Updates(map[string]any{
				"status":        model.TaskStatusPaused,
				"error_message": "服务重启，任务已暂停",
				"updated_at":    now,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&taskStepRow{}).Where("status = ?", model.StepStatusRunning).
			Updates(map[string]any{"status": model.StepStatusPaused, "ended_at": now}).Error
	})
}

/* ---- 行 <-> 模型转换 ---- */

func taskToRow(t *model.Task) *taskRow {
	return &taskRow{
		ID: t.ID, Type: t.Type, Title: t.Title, Status: t.Status, CurrentStep: t.CurrentStep,
		Workflow: strPtr(t.Workflow), Input: strPtr(t.Input), Output: strPtr(t.Output),
		AgentContext: strPtr(t.AgentContext),
		ItemsCount: t.ItemsCount, ImportedCount: t.ImportedCount, FailedCount: t.FailedCount,
		TargetProjectID: strPtr(t.TargetProjectID), TargetProjectName: strPtr(t.TargetProjectName),
		OutputDatasetID: strPtr(t.OutputDatasetID), InputDatasetID: strPtr(t.InputDatasetID),
		ErrorMessage: strPtr(t.ErrorMessage),
		CreatedAt:    t.CreatedAt, UpdatedAt: t.UpdatedAt,
		StartedAt: timePtr(t.StartedAt), FinishedAt: timePtr(t.FinishedAt),
	}
}

func taskToModel(row *taskRow) model.Task {
	return model.Task{
		ID: row.ID, Type: row.Type, Title: row.Title, Status: row.Status, CurrentStep: row.CurrentStep,
		Workflow: strVal(row.Workflow), Input: strVal(row.Input), Output: strVal(row.Output),
		AgentContext: strVal(row.AgentContext),
		ItemsCount: row.ItemsCount, ImportedCount: row.ImportedCount, FailedCount: row.FailedCount,
		TargetProjectID: strVal(row.TargetProjectID), TargetProjectName: strVal(row.TargetProjectName),
		OutputDatasetID: strVal(row.OutputDatasetID), InputDatasetID: strVal(row.InputDatasetID),
		ErrorMessage: strVal(row.ErrorMessage),
		CreatedAt:    row.CreatedAt, UpdatedAt: row.UpdatedAt,
		StartedAt: timeVal(row.StartedAt), FinishedAt: timeVal(row.FinishedAt),
	}
}

func stepToRow(s model.TaskStep, taskID string) taskStepRow {
	return taskStepRow{
		ID: s.ID, TaskID: taskID, Seq: s.Seq, Name: s.Name, Status: s.Status,
		Detail: s.Detail, Data: strPtr(s.Data),
		StartedAt: timePtr(s.StartedAt), EndedAt: timePtr(s.EndedAt),
	}
}

func stepRowToModel(row *taskStepRow) model.TaskStep {
	return model.TaskStep{
		ID: row.ID, TaskID: row.TaskID, Seq: row.Seq, Name: row.Name, Status: row.Status,
		Detail: row.Detail, Data: strVal(row.Data),
		StartedAt: timeVal(row.StartedAt), EndedAt: timeVal(row.EndedAt),
	}
}

func itemToRow(id, taskID string, it model.TaskItem) taskItemRow {
	return taskItemRow{
		ID: id, TaskID: taskID, Fields: it.Fields,
		Status: it.Status, ErrorMessage: strPtr(it.ErrorMessage),
	}
}

func itemRowToModel(row *taskItemRow) model.TaskItem {
	return model.TaskItem{
		ID: row.ID, TaskID: row.TaskID, Fields: row.Fields,
		Status: row.Status, ErrorMessage: strVal(row.ErrorMessage),
	}
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func timeVal(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}
