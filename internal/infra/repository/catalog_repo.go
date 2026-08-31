package repository

import (
	"context"
	"fmt"

	"reqflow/internal/domain/model"
)

func (r *PipelineRepo) ListTaskDefinitions(ctx context.Context, workspaceID, status string,
	limit int) ([]model.TaskDefinition, error) {
	limit = catalogLimit(limit)
	query := r.db.WithContext(ctx).Where("workspace_id = ?", workspaceID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var rows []taskDefinitionV2Row
	if err := query.Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.TaskDefinition, len(rows))
	for i := range rows {
		definition, err := rows[i].toModel()
		if err != nil {
			return nil, err
		}
		out[i] = *definition
	}
	return out, nil
}

func (r *PipelineRepo) ListDatasetSchemas(ctx context.Context, workspaceID string,
	limit int) ([]model.DatasetSchemaDefinition, error) {
	var rows []pipelineSchemaRow
	if err := r.db.WithContext(ctx).Where("workspace_id = ?", workspaceID).
		Order("created_at DESC, id DESC").Limit(catalogLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.DatasetSchemaDefinition, len(rows))
	for i := range rows {
		out[i] = *rows[i].toModel()
	}
	return out, nil
}

func (r *PipelineRepo) ListAppendDatasets(ctx context.Context, workspaceID, status string,
	purpose model.DatasetPurpose, limit int) ([]model.Dataset, error) {
	query := r.db.WithContext(ctx).Where("workspace_id = ? AND schema_id IS NOT NULL", workspaceID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if purpose != "" {
		query = query.Where("purpose = ?", purpose)
	}
	var rows []pipelineDatasetRow
	if err := query.Order("created_at DESC, id DESC").Limit(catalogLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.Dataset, len(rows))
	for i := range rows {
		out[i] = *rows[i].toModel()
	}
	return out, nil
}

func (r *PipelineRepo) ListDatasetBatches(ctx context.Context, datasetID string,
	limit int) ([]model.DatasetBatch, error) {
	var rows []pipelineBatchRow
	if err := r.db.WithContext(ctx).Where("dataset_id = ?", datasetID).
		Order("created_at DESC, id DESC").Limit(catalogLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.DatasetBatch, len(rows))
	for i := range rows {
		out[i] = *rows[i].toModel()
	}
	return out, nil
}

func (r *PipelineRepo) SetAppendDatasetStatus(ctx context.Context, datasetID, fromStatus, toStatus string) error {
	result := r.db.WithContext(ctx).Model(&pipelineDatasetRow{}).
		Where("id = ? AND schema_id IS NOT NULL AND status = ?", datasetID, fromStatus).
		Update("status", toStatus)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("Dataset 不存在或当前状态不允许此操作")
	}
	return nil
}

func (r *PipelineRepo) ListAssetSets(ctx context.Context, workspaceID string, limit int) ([]model.AssetSet, error) {
	var rows []assetSetV2Row
	if err := r.db.WithContext(ctx).Where("workspace_id = ? AND created_by NOT LIKE ?", workspaceID, "task_batch:%").
		Order("created_at DESC, id DESC").Limit(catalogLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.AssetSet, len(rows))
	for i := range rows {
		out[i] = rows[i].toModel()
	}
	return out, nil
}

func (r *PipelineRepo) ListExtractionProfiles(ctx context.Context, workspaceID string,
	limit int) ([]model.ExtractionProfile, error) {
	var rows []extractionProfileRow
	if err := r.db.WithContext(ctx).Where("workspace_id = ?", workspaceID).
		Order("created_at DESC, id DESC").Limit(catalogLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.ExtractionProfile, len(rows))
	for i := range rows {
		out[i] = rows[i].toModel()
	}
	return out, nil
}

func (r *PipelineRepo) ArchiveOrchestratorTask(ctx context.Context, taskID string) error {
	result := r.db.WithContext(ctx).Exec(`UPDATE tasks SET archived_at = now(), updated_at = now()
		WHERE id = ? AND definition_id IS NOT NULL AND archived_at IS NULL AND status IN (?, ?)`,
		taskID, model.TaskStatusSucceeded, model.TaskStatusFailed)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("只有已完成或失败的 V2 Task 可以归档")
	}
	return nil
}

func (r *PipelineRepo) RestoreOrchestratorTask(ctx context.Context, taskID string) error {
	result := r.db.WithContext(ctx).Exec(`UPDATE tasks SET archived_at = NULL, updated_at = now()
		WHERE id = ? AND definition_id IS NOT NULL AND archived_at IS NOT NULL`, taskID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("归档中不存在该 V2 Task")
	}
	return nil
}

func (r *PipelineRepo) ListArchivedOrchestratorTasks(ctx context.Context, workspaceID string,
	limit int) ([]model.Task, error) {
	var rows []taskRow
	if err := r.db.WithContext(ctx).Where("workspace_id = ? AND definition_id IS NOT NULL AND archived_at IS NOT NULL", workspaceID).
		Order("archived_at DESC, id DESC").Limit(catalogLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.Task, len(rows))
	for i := range rows {
		out[i] = taskToModel(&rows[i])
	}
	return out, nil
}

func catalogLimit(limit int) int {
	if limit < 1 || limit > 200 {
		return 100
	}
	return limit
}
