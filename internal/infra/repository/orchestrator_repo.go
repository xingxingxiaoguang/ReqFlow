package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

func (r *PipelineRepo) CreateTaskDefinition(ctx context.Context, definition *model.TaskDefinition, snapshot []byte) error {
	if definition.ID == "" {
		definition.ID = uuid.NewString()
	}
	now := time.Now()
	definition.CreatedAt, definition.UpdatedAt = now, now
	return r.db.WithContext(ctx).Exec(`INSERT INTO task_definitions
		(id, workspace_id, key, name, description, status, definition, definition_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?::jsonb, ?, ?, ?)`,
		definition.ID, definition.WorkspaceID, definition.Key, definition.Name,
		definition.Description, definition.Status, string(snapshot), definition.DefinitionHash,
		definition.CreatedAt, definition.UpdatedAt).Error
}

func (r *PipelineRepo) GetTaskDefinition(ctx context.Context, id string) (*model.TaskDefinition, error) {
	var row taskDefinitionV2Row
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	return row.toModel()
}

func (r *PipelineRepo) ResolveTaskResource(ctx context.Context, workspaceID string, binding model.TaskResourceBinding, alias string) (model.TaskResourceBinding, error) {
	if workspaceID == "" {
		workspaceID = "default"
	}
	if alias != "" && binding.ResourceID != "" {
		return binding, fmt.Errorf("resource_id 与 resource_alias 不能同时提供")
	}
	switch binding.ResourceType {
	case model.ResourceAssetSet:
		if alias != "" {
			return binding, fmt.Errorf("asset_set 不支持 alias")
		}
		var count int64
		if err := r.db.WithContext(ctx).Raw(`SELECT count(*) FROM asset_sets WHERE id = ? AND workspace_id = ?`,
			binding.ResourceID, workspaceID).Scan(&count).Error; err != nil || count != 1 {
			if err != nil {
				return binding, err
			}
			return binding, fmt.Errorf("asset_set %s 不存在", binding.ResourceID)
		}
	case model.ResourceParsedDocuments:
		if alias != "" {
			return binding, fmt.Errorf("parsed_documents 不支持 alias")
		}
		var row struct {
			ID            string
			AssetSetID    string
			ParserName    string
			ParserVersion string
			Status        string
		}
		if err := r.db.WithContext(ctx).Table("parsed_document_sets AS p").
			Select("p.id, p.asset_set_id, p.parser_name, p.parser_version, p.status").
			Joins("JOIN asset_sets AS a ON a.id = p.asset_set_id").
			Where("p.id = ? AND a.workspace_id = ?", binding.ResourceID, workspaceID).Take(&row).Error; err != nil {
			return binding, fmt.Errorf("ParsedDocumentSet 资源不存在: %w", err)
		}
		if row.Status != model.ParsedDocumentSetSucceeded {
			return binding, fmt.Errorf("ParsedDocumentSet %s 状态 %s 不可作为任务输入", row.ID, row.Status)
		}
		boundary, _ := json.Marshal(model.ParsedDocumentsBoundary{AssetSetID: row.AssetSetID,
			ParserName: row.ParserName, ParserVersion: row.ParserVersion})
		binding.Boundary = boundary
	case model.ResourceRecordDrafts:
		if alias != "" {
			return binding, fmt.Errorf("record_drafts 不支持 alias")
		}
		var row struct {
			ID                  string
			ParsedDocumentSetID string
			ExtractionProfileID string
			TargetSchemaID      string
			ProfileHash         string
			Model               string
			Status              string
		}
		if err := r.db.WithContext(ctx).Table("record_draft_sets AS d").
			Select(`d.id, d.parsed_document_set_id, d.extraction_profile_id,
				p.target_schema_id, p.profile_hash, d.model, d.status`).
			Joins("JOIN extraction_profiles AS p ON p.id = d.extraction_profile_id").
			Where("d.id = ? AND p.workspace_id = ?", binding.ResourceID, workspaceID).
			Take(&row).Error; err != nil {
			return binding, fmt.Errorf("RecordDraftSet 资源不存在: %w", err)
		}
		if row.Status != model.RecordDraftSetSucceeded {
			return binding, fmt.Errorf("RecordDraftSet %s 状态 %s 不可作为任务输入", row.ID, row.Status)
		}
		boundary, _ := json.Marshal(model.RecordDraftsBoundary{
			ParsedDocumentSetID: row.ParsedDocumentSetID,
			ExtractionProfileID: row.ExtractionProfileID, TargetSchemaID: row.TargetSchemaID,
			ProfileHash: row.ProfileHash, Model: row.Model,
		})
		binding.Boundary = boundary
	case model.ResourceTransformedRecords:
		if alias != "" {
			return binding, fmt.Errorf("transformed_records 不支持 alias")
		}
		var row struct {
			ID                  string
			RecordDraftSetID    string
			ExtractionProfileID string
			TargetSchemaID      string
			ProfileHash         string
			EngineVersion       string
			Status              string
		}
		if err := r.db.WithContext(ctx).Table("transformed_record_sets AS t").
			Select(`t.id, t.record_draft_set_id, t.extraction_profile_id, t.target_schema_id,
				p.profile_hash, t.engine_version, t.status`).
			Joins("JOIN extraction_profiles AS p ON p.id = t.extraction_profile_id").
			Where("t.id = ? AND p.workspace_id = ?", binding.ResourceID, workspaceID).
			Take(&row).Error; err != nil {
			return binding, fmt.Errorf("TransformedRecordSet 资源不存在: %w", err)
		}
		if row.Status != model.TransformedRecordSetSucceeded {
			return binding, fmt.Errorf("TransformedRecordSet %s 状态 %s 不可作为任务输入", row.ID, row.Status)
		}
		boundary, _ := json.Marshal(model.TransformedRecordsBoundary{RecordDraftSetID: row.RecordDraftSetID,
			ExtractionProfileID: row.ExtractionProfileID, TargetSchemaID: row.TargetSchemaID,
			ProfileHash: row.ProfileHash, TransformEngineVersion: row.EngineVersion})
		binding.Boundary = boundary
	case model.ResourceValidationResults:
		if alias != "" {
			return binding, fmt.Errorf("validation_results 不支持 alias")
		}
		var row struct {
			ID                     string
			TransformedRecordSetID string
			TargetDatasetID        string
			TargetSchemaID         string
			ValidatedThroughSeq    int64
			EngineVersion          string
			Status                 string
		}
		if err := r.db.WithContext(ctx).Table("validation_result_sets AS v").
			Select(`v.id, v.transformed_record_set_id, v.target_dataset_id, v.target_schema_id,
				v.validated_through_seq, v.engine_version, v.status`).
			Joins("JOIN datasets AS d ON d.id = v.target_dataset_id").
			Where("v.id = ? AND d.workspace_id = ?", binding.ResourceID, workspaceID).
			Take(&row).Error; err != nil {
			return binding, fmt.Errorf("ValidationResultSet 资源不存在: %w", err)
		}
		if row.Status != model.ValidationResultSetSucceeded {
			return binding, fmt.Errorf("ValidationResultSet %s 状态 %s 不可作为任务输入", row.ID, row.Status)
		}
		boundary, _ := json.Marshal(model.ValidationResultsBoundary{
			TransformedRecordSetID: row.TransformedRecordSetID, TargetDatasetID: row.TargetDatasetID,
			TargetSchemaID: row.TargetSchemaID, ValidatedThroughSeq: row.ValidatedThroughSeq,
			ValidationEngineVersion: row.EngineVersion})
		binding.Boundary = boundary
	case model.ResourceApprovedRecords:
		if alias != "" {
			return binding, fmt.Errorf("approved_records 不支持 alias")
		}
		var row struct {
			ID                    string
			ValidationResultSetID string
			TargetDatasetID       string
			TargetSchemaID        string
			ReviewedThroughSeq    int64
			ReviewHash            string
		}
		if err := r.db.WithContext(ctx).Table("approved_record_sets AS a").
			Select(`a.id, a.validation_result_set_id, a.target_dataset_id, a.target_schema_id,
				a.reviewed_through_seq, a.review_hash`).
			Joins("JOIN datasets AS d ON d.id = a.target_dataset_id").
			Where("a.id = ? AND d.workspace_id = ?", binding.ResourceID, workspaceID).
			Take(&row).Error; err != nil {
			return binding, fmt.Errorf("ApprovedRecordSet 资源不存在: %w", err)
		}
		boundary, _ := json.Marshal(model.ApprovedRecordsBoundary{
			ValidationResultSetID: row.ValidationResultSetID, TargetDatasetID: row.TargetDatasetID,
			TargetSchemaID: row.TargetSchemaID, ReviewedThroughSeq: row.ReviewedThroughSeq,
			ReviewHash: row.ReviewHash})
		binding.Boundary = boundary
	case model.ResourceDataset, model.ResourceDatasetBoundary:
		var row struct {
			ID         string
			CurrentSeq int64
			Status     string
		}
		query := r.db.WithContext(ctx).Table("datasets AS d").
			Select("d.id, d.current_seq, d.status").
			Where("d.workspace_id = ? AND d.schema_id IS NOT NULL", workspaceID)
		if alias != "" {
			query = query.Joins("JOIN dataset_aliases AS a ON a.active_dataset_id = d.id").Where("a.workspace_id = ? AND a.name = ?", workspaceID, alias)
		} else {
			query = query.Where("d.id = ?", binding.ResourceID)
		}
		if err := query.Take(&row).Error; err != nil {
			return binding, fmt.Errorf("Dataset 资源不存在: %w", err)
		}
		if row.Status != model.DatasetStatusActive {
			return binding, fmt.Errorf("Dataset %s 当前状态 %s 不可绑定", row.ID, row.Status)
		}
		binding.ResourceID = row.ID
		if binding.ResourceType == model.ResourceDatasetBoundary {
			boundary, _ := json.Marshal(model.DatasetBoundary{DatasetID: row.ID, ThroughSeq: row.CurrentSeq})
			binding.Boundary = boundary
		}
	case model.ResourceDatasetBatch:
		if alias != "" {
			return binding, fmt.Errorf("dataset_batch 不支持 alias")
		}
		var batch struct {
			DatasetID string
			Status    string
			FromSeq   int64
			ToSeq     int64
		}
		if err := r.db.WithContext(ctx).Table("dataset_batches AS b").
			Select("b.dataset_id, b.status, b.from_seq, b.to_seq").
			Joins("JOIN datasets AS d ON d.id = b.dataset_id").
			Where("b.id = ? AND d.workspace_id = ?", binding.ResourceID, workspaceID).
			Take(&batch).Error; err != nil {
			return binding, fmt.Errorf("DatasetBatch 资源不存在: %w", err)
		}
		if batch.Status != model.DatasetBatchCommitted {
			return binding, fmt.Errorf("DatasetBatch %s 未提交或不存在", binding.ResourceID)
		}
		boundary, _ := json.Marshal(model.DatasetBatchBoundary{DatasetID: batch.DatasetID,
			FromSeq: batch.FromSeq, ToSeq: batch.ToSeq})
		binding.Boundary = boundary
	case model.ResourcePipelineCursor:
		if alias != "" {
			return binding, fmt.Errorf("pipeline_cursor 不支持 alias")
		}
		var cursor struct {
			PipelineKey         string
			SourceDatasetID     string
			TargetDatasetID     string
			ProcessedThroughSeq int64
			LastSuccessTaskID   *string
			TargetThroughSeq    int64
		}
		if err := r.db.WithContext(ctx).Table("pipeline_cursors AS c").
			Select(`c.pipeline_key, c.source_dataset_id, c.target_dataset_id,
				c.processed_through_seq, c.last_success_task_id, target.current_seq AS target_through_seq`).
			Joins("JOIN datasets AS source ON source.id = c.source_dataset_id").
			Joins("JOIN datasets AS target ON target.id = c.target_dataset_id").
			Where("c.id = ? AND source.workspace_id = ? AND target.workspace_id = ?",
				binding.ResourceID, workspaceID, workspaceID).Take(&cursor).Error; err != nil {
			return binding, fmt.Errorf("PipelineCursor 资源不存在: %w", err)
		}
		var targetBatchID string
		if cursor.LastSuccessTaskID != nil {
			_ = r.db.WithContext(ctx).Model(&pipelineBatchRow{}).Select("id").
				Where("dataset_id = ? AND source_task_id = ? AND status = ?", cursor.TargetDatasetID,
					*cursor.LastSuccessTaskID, model.DatasetBatchCommitted).
				Order("committed_at DESC").Limit(1).Scan(&targetBatchID).Error
		}
		boundary, _ := json.Marshal(model.PipelineCursorBoundary{PipelineKey: cursor.PipelineKey,
			SourceDatasetID: cursor.SourceDatasetID, TargetDatasetID: cursor.TargetDatasetID,
			ProcessedThroughSeq: cursor.ProcessedThroughSeq, TargetBatchID: targetBatchID,
			TargetThroughSeq: cursor.TargetThroughSeq})
		binding.Boundary = boundary
	case model.ResourceRetrievalSnapshot:
		if alias != "" {
			return binding, fmt.Errorf("retrieval_snapshot 不支持 alias")
		}
		var snapshot struct {
			ID        string
			SourceSeq int64
			Status    string
		}
		if err := r.db.WithContext(ctx).Raw(`SELECT id, source_seq, status FROM retrieval_snapshots WHERE id = ?`,
			binding.ResourceID).Scan(&snapshot).Error; err != nil {
			return binding, err
		}
		if snapshot.ID == "" || snapshot.Status != model.RetrievalSnapshotActive {
			return binding, fmt.Errorf("RetrievalSnapshot %s 未激活或不存在", binding.ResourceID)
		}
		boundary, _ := json.Marshal(model.RetrievalBoundary{RetrievalSnapshotID: snapshot.ID, SourceSeq: snapshot.SourceSeq})
		binding.Boundary = boundary
	case model.ResourceArtifact:
		if alias != "" {
			return binding, fmt.Errorf("artifact 不支持 alias")
		}
		var count int64
		if err := r.db.WithContext(ctx).Raw(`SELECT count(*) FROM artifacts WHERE id = ? AND workspace_id = ?`,
			binding.ResourceID, workspaceID).Scan(&count).Error; err != nil || count != 1 {
			if err != nil {
				return binding, err
			}
			return binding, fmt.Errorf("Artifact %s 不存在", binding.ResourceID)
		}
	default:
		return binding, fmt.Errorf("资源类型 %s 尚不能作为 Task 直接输入", binding.ResourceType)
	}
	return binding, nil
}

func (r *PipelineRepo) CreateTaskExecution(ctx context.Context, task *model.Task, bindings []model.TaskResourceBinding, steps []model.StepRun) error {
	if task.ID == "" {
		task.ID = uuid.NewString()
	}
	now := time.Now()
	task.CreatedAt, task.UpdatedAt = now, now
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO tasks
			(id, workspace_id, definition_id, definition_snapshot, type, title, status,
			 current_step, created_at, updated_at)
			VALUES (?, ?, ?, ?::jsonb, ?, ?, ?, 0, ?, ?)`,
			task.ID, task.WorkspaceID, task.DefinitionID, task.DefinitionSnapshot,
			task.Type, task.Title, task.Status, task.CreatedAt, task.UpdatedAt).Error; err != nil {
			return err
		}
		for i := range bindings {
			binding := &bindings[i]
			if binding.ID == "" {
				binding.ID = uuid.NewString()
			}
			binding.TaskID = task.ID
			binding.CreatedAt = now
			boundary := binding.Boundary
			if len(boundary) == 0 {
				boundary = json.RawMessage(`{}`)
			}
			if err := tx.Exec(`INSERT INTO task_resource_bindings
				(id, task_id, port_name, direction, resource_type, resource_id, boundary, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?::jsonb, ?)`,
				binding.ID, binding.TaskID, binding.PortName, binding.Direction,
				binding.ResourceType, binding.ResourceID, string(boundary), binding.CreatedAt).Error; err != nil {
				return err
			}
		}
		for i := range steps {
			step := &steps[i]
			if step.ID == "" {
				step.ID = uuid.NewString()
			}
			step.TaskID = task.ID
			step.CreatedAt = now
			checkpoint := step.Checkpoint
			if len(checkpoint) == 0 {
				checkpoint = json.RawMessage(`{}`)
			}
			progress := step.Progress
			if len(progress) == 0 {
				progress = json.RawMessage(`{}`)
			}
			if err := tx.Exec(`INSERT INTO step_runs
				(id, task_id, step_id, ordinal, kind, status, attempt, input_hash, config_hash,
				 checkpoint, progress, error_code, error_message, lease_owner, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?::jsonb, ?::jsonb, ?, ?, ?, ?)`,
				step.ID, step.TaskID, step.StepID, step.Ordinal, step.Kind, step.Status, step.Attempt,
				step.InputHash, step.ConfigHash, string(checkpoint), string(progress),
				step.ErrorCode, step.ErrorMessage, step.LeaseOwner, step.CreatedAt).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *PipelineRepo) GetTaskResourceBindings(ctx context.Context, taskID string) ([]model.TaskResourceBinding, error) {
	var rows []taskResourceBindingRow
	if err := r.db.WithContext(ctx).Where("task_id = ?", taskID).
		Order("direction ASC, port_name ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.TaskResourceBinding, len(rows))
	for i := range rows {
		out[i] = rows[i].toModel()
	}
	return out, nil
}

func (r *PipelineRepo) GetStepRuns(ctx context.Context, taskID string) ([]model.StepRun, error) {
	var rows []stepRunV2Row
	if err := r.db.WithContext(ctx).Where("task_id = ?", taskID).
		Order("ordinal ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.StepRun, len(rows))
	for i := range rows {
		out[i] = rows[i].toModel()
	}
	return out, nil
}

func (r *PipelineRepo) GetTaskExecution(ctx context.Context, taskID string) (*model.TaskExecution, error) {
	var row taskRow
	if err := r.db.WithContext(ctx).Where("id = ? AND definition_id IS NOT NULL", taskID).First(&row).Error; err != nil {
		return nil, err
	}
	inputs, err := r.GetTaskResourceBindings(ctx, taskID)
	if err != nil {
		return nil, err
	}
	steps, err := r.GetStepRuns(ctx, taskID)
	if err != nil {
		return nil, err
	}
	outputs, err := r.GetStepResourceBindings(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return &model.TaskExecution{
		Task: taskToModel(&row), Inputs: inputs, Steps: steps, StepOutputs: outputs,
	}, nil
}

func (r *PipelineRepo) ListOrchestratorTasks(ctx context.Context, filter port.OrchestratorTaskFilter) ([]model.Task, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := r.db.WithContext(ctx).Where("definition_id IS NOT NULL")
	if filter.WorkspaceID != "" {
		query = query.Where("workspace_id = ?", filter.WorkspaceID)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	var rows []taskRow
	if err := query.Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	tasks := make([]model.Task, len(rows))
	for i := range rows {
		tasks[i] = taskToModel(&rows[i])
	}
	return tasks, nil
}

func (r *PipelineRepo) GetStepResourceBindings(ctx context.Context, taskID string) ([]model.StepResourceBinding, error) {
	var rows []stepResourceBindingRow
	err := r.db.WithContext(ctx).Table("step_resource_bindings AS b").
		Select("b.*").Joins("JOIN step_runs AS s ON s.id = b.step_run_id").
		Where("s.task_id = ?", taskID).Order("s.ordinal ASC, b.port_name ASC").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]model.StepResourceBinding, len(rows))
	for i := range rows {
		out[i] = rows[i].toModel()
	}
	return out, nil
}

func (r *PipelineRepo) ListSchedulableTaskIDs(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var ids []string
	err := r.db.WithContext(ctx).Raw(`SELECT id FROM tasks
		WHERE definition_id IS NOT NULL AND status = ? ORDER BY updated_at ASC LIMIT ?`,
		model.TaskStatusRunning, limit).Scan(&ids).Error
	return ids, err
}

func (r *PipelineRepo) StartTask(ctx context.Context, taskID string) error {
	now := time.Now()
	result := r.db.WithContext(ctx).Exec(`UPDATE tasks
		SET status = ?, started_at = COALESCE(started_at, ?), finished_at = NULL,
			error_message = NULL, updated_at = ?
		WHERE id = ? AND definition_id IS NOT NULL AND status = ?`,
		model.TaskStatusRunning, now, now, taskID, model.TaskStatusPending)
	return transitionResult(result)
}

// RequestTaskPause 使用 pausing 过渡态：排队/人工步骤立即暂停，运行步骤保留有效
// lease 以便 Worker 写最后检查点；续租会返回 ErrPauseRequested 触发远端取消。
func (r *PipelineRepo) RequestTaskPause(ctx context.Context, taskID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		result := tx.Exec(`UPDATE tasks SET status = ?, updated_at = ?
			WHERE id = ? AND definition_id IS NOT NULL AND status IN (?, ?)`,
			model.TaskStatusPausing, now, taskID, model.TaskStatusRunning, model.TaskStatusAwaiting)
		if err := transitionResult(result); err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE step_runs SET status = ?, lease_owner = '', lease_until = NULL
			WHERE task_id = ? AND status IN (?, ?)`, model.StepRunPaused, taskID,
			model.StepRunQueued, model.StepRunAwaiting).Error; err != nil {
			return err
		}
		return settlePausedTask(tx, taskID, now)
	})
}

func (r *PipelineRepo) ResumeTask(ctx context.Context, taskID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		result := tx.Exec(`UPDATE tasks SET status = ?, finished_at = NULL, error_message = NULL, updated_at = ?
			WHERE id = ? AND definition_id IS NOT NULL AND status = ?`,
			model.TaskStatusRunning, now, taskID, model.TaskStatusPaused)
		if err := transitionResult(result); err != nil {
			return err
		}
		return tx.Exec(`UPDATE step_runs SET status = ?, lease_owner = '', lease_until = NULL,
			error_code = '', error_message = '', finished_at = NULL
			WHERE task_id = ? AND status = ?`, model.StepRunPending, taskID, model.StepRunPaused).Error
	})
}

func (r *PipelineRepo) RetryStep(ctx context.Context, taskID, stepID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		result := tx.Exec(`UPDATE step_runs SET status = ?, lease_owner = '', lease_until = NULL,
			error_code = '', error_message = '', finished_at = NULL
			WHERE task_id = ? AND step_id = ? AND status = ?`,
			model.StepRunPending, taskID, stepID, model.StepRunFailed)
		if err := transitionResult(result); err != nil {
			return err
		}
		result = tx.Exec(`UPDATE tasks SET status = ?, finished_at = NULL, error_message = NULL, updated_at = ?
			WHERE id = ? AND status = ?`, model.TaskStatusRunning, now, taskID, model.TaskStatusFailed)
		return transitionResult(result)
	})
}

func (r *PipelineRepo) QueueReadySteps(ctx context.Context, taskID string, queued, awaiting []port.StepQueueEntry) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var status string
		if err := tx.Raw(`SELECT status FROM tasks WHERE id = ? FOR UPDATE`, taskID).Scan(&status).Error; err != nil {
			return err
		}
		if status != model.TaskStatusRunning {
			// Scheduler 基于旧快照重入时，另一实例可能已经收敛了状态。
			return nil
		}
		for _, entry := range queued {
			if err := tx.Exec(`UPDATE step_runs SET status = ?,
				checkpoint = CASE WHEN input_hash = '' OR input_hash = ? THEN checkpoint ELSE '{}'::jsonb END,
				progress = CASE WHEN input_hash = '' OR input_hash = ? THEN progress ELSE '{}'::jsonb END,
				input_hash = ? WHERE task_id = ? AND id = ? AND status = ?`, model.StepRunQueued,
				entry.InputHash, entry.InputHash, entry.InputHash, taskID, entry.StepRunID, model.StepRunPending).Error; err != nil {
				return err
			}
		}
		for _, entry := range awaiting {
			if err := tx.Exec(`UPDATE step_runs SET status = ?,
				checkpoint = CASE WHEN input_hash = '' OR input_hash = ? THEN checkpoint ELSE '{}'::jsonb END,
				progress = CASE WHEN input_hash = '' OR input_hash = ? THEN progress ELSE '{}'::jsonb END,
				input_hash = ? WHERE task_id = ? AND id = ? AND status = ?`, model.StepRunAwaiting,
				entry.InputHash, entry.InputHash, entry.InputHash, taskID, entry.StepRunID, model.StepRunPending).Error; err != nil {
				return err
			}
		}
		var executable int64
		if err := tx.Raw(`SELECT count(*) FROM step_runs WHERE task_id = ? AND status IN (?, ?)`,
			taskID, model.StepRunQueued, model.StepRunRunning).Scan(&executable).Error; err != nil {
			return err
		}
		if executable == 0 {
			var awaiting int64
			if err := tx.Raw(`SELECT count(*) FROM step_runs WHERE task_id = ? AND status = ?`,
				taskID, model.StepRunAwaiting).Scan(&awaiting).Error; err != nil {
				return err
			}
			if awaiting > 0 {
				return tx.Exec(`UPDATE tasks SET status = ?, updated_at = ? WHERE id = ? AND status = ?`,
					model.TaskStatusAwaiting, time.Now(), taskID, model.TaskStatusRunning).Error
			}
		}
		return nil
	})
}

func (r *PipelineRepo) CompleteAwaitingStep(ctx context.Context, stepRunID string, outputs []model.StepResourceBinding) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var taskID string
		now := time.Now()
		result := tx.Raw(`UPDATE step_runs SET status = ?, finished_at = ?, error_code = '', error_message = ''
			WHERE id = ? AND status = ? RETURNING task_id`,
			model.StepRunSucceeded, now, stepRunID, model.StepRunAwaiting).Scan(&taskID)
		if err := result.Error; err != nil {
			return err
		}
		if taskID == "" {
			return port.ErrInvalidTransition
		}
		if err := writeStepOutputs(tx, stepRunID, outputs, now); err != nil {
			return err
		}
		return tx.Exec(`UPDATE tasks SET status = ?, updated_at = ? WHERE id = ? AND status = ?`,
			model.TaskStatusRunning, now, taskID, model.TaskStatusAwaiting).Error
	})
}

func (r *PipelineRepo) CompleteTask(ctx context.Context, taskID string, outputs []model.TaskResourceBinding) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var taskStatus string
		if err := tx.Raw(`SELECT status FROM tasks WHERE id = ? FOR UPDATE`, taskID).Scan(&taskStatus).Error; err != nil {
			return err
		}
		if taskStatus != model.TaskStatusRunning && taskStatus != model.TaskStatusAwaiting {
			if taskStatus == model.TaskStatusSucceeded {
				return nil
			}
			return port.ErrInvalidTransition
		}
		var incomplete int64
		if err := tx.Raw(`SELECT count(*) FROM step_runs WHERE task_id = ? AND status NOT IN (?, ?)`,
			taskID, model.StepRunSucceeded, model.StepRunSkipped).Scan(&incomplete).Error; err != nil {
			return err
		}
		if incomplete != 0 {
			return port.ErrInvalidTransition
		}
		now := time.Now()
		for i := range outputs {
			binding := outputs[i]
			if binding.ID == "" {
				binding.ID = uuid.NewString()
			}
			boundary := jsonOrEmpty(binding.Boundary)
			if err := tx.Exec(`INSERT INTO task_resource_bindings
				(id, task_id, port_name, direction, resource_type, resource_id, boundary, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?::jsonb, ?)`, binding.ID, taskID, binding.PortName,
				model.ResourceOutput, binding.ResourceType, binding.ResourceID, string(boundary), now).Error; err != nil {
				return err
			}
		}
		return tx.Exec(`UPDATE tasks SET status = ?, finished_at = ?, error_message = NULL, updated_at = ? WHERE id = ?`,
			model.TaskStatusSucceeded, now, now, taskID).Error
	})
}

func (r *PipelineRepo) ClaimStep(ctx context.Context, owner string, leaseUntil time.Time) (*model.StepRun, error) {
	var claimed model.StepRun
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row stepRunV2Row
		err := tx.Raw(`SELECT s.* FROM step_runs AS s
			JOIN tasks AS t ON t.id = s.task_id
			WHERE s.status = ? AND t.status = ?
			ORDER BY s.created_at ASC, s.ordinal ASC
			FOR UPDATE OF s SKIP LOCKED LIMIT 1`, model.StepRunQueued, model.TaskStatusRunning).Scan(&row).Error
		if err != nil {
			return err
		}
		if row.ID == "" {
			return port.ErrNoRunnableStep
		}
		now := time.Now()
		result := tx.Exec(`UPDATE step_runs SET status = ?, attempt = attempt + 1,
			lease_owner = ?, lease_until = ?, started_at = COALESCE(started_at, ?), finished_at = NULL
			WHERE id = ? AND status = ?`, model.StepRunRunning, owner, leaseUntil, now,
			row.ID, model.StepRunQueued)
		if err := transitionResult(result); err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE tasks SET current_step = ?, updated_at = ? WHERE id = ?`,
			row.Ordinal, now, row.TaskID).Error; err != nil {
			return err
		}
		row.Status, row.Attempt, row.LeaseOwner, row.LeaseUntil = model.StepRunRunning, row.Attempt+1, owner, &leaseUntil
		if row.StartedAt == nil {
			row.StartedAt = &now
		}
		claimed = row.toModel()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &claimed, nil
}

func (r *PipelineRepo) RenewStepLease(ctx context.Context, stepRunID, owner string, leaseUntil time.Time) error {
	now := time.Now()
	result := r.db.WithContext(ctx).Exec(`UPDATE step_runs AS s SET lease_until = ?
		FROM tasks AS t WHERE s.task_id = t.id AND s.id = ? AND s.status = ?
		AND s.lease_owner = ? AND s.lease_until > ? AND t.status = ?`,
		leaseUntil, stepRunID, model.StepRunRunning, owner, now, model.TaskStatusRunning)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	var taskStatus string
	_ = r.db.WithContext(ctx).Raw(`SELECT t.status FROM tasks t JOIN step_runs s ON s.task_id = t.id
		WHERE s.id = ? AND s.status = ? AND s.lease_owner = ? AND s.lease_until > ?`,
		stepRunID, model.StepRunRunning, owner, now).Scan(&taskStatus).Error
	if taskStatus == model.TaskStatusPausing {
		return port.ErrPauseRequested
	}
	return port.ErrLeaseLost
}

func (r *PipelineRepo) SaveStepCheckpoint(ctx context.Context, stepRunID, owner string, checkpoint json.RawMessage) error {
	return r.saveOwnedJSON(ctx, stepRunID, owner, "checkpoint", checkpoint)
}

func (r *PipelineRepo) SaveStepProgress(ctx context.Context, stepRunID, owner string, progress json.RawMessage) error {
	return r.saveOwnedJSON(ctx, stepRunID, owner, "progress", progress)
}

func (r *PipelineRepo) saveOwnedJSON(ctx context.Context, stepRunID, owner, column string, payload json.RawMessage) error {
	if !json.Valid(payload) {
		return fmt.Errorf("%s 不是合法 JSON", column)
	}
	// column 仅由上面两个固定调用点传入，不接受外部输入。
	query := fmt.Sprintf(`UPDATE step_runs SET %s = ?::jsonb WHERE id = ? AND status = ?
		AND lease_owner = ? AND lease_until > ?`, column)
	result := r.db.WithContext(ctx).Exec(query, string(payload), stepRunID, model.StepRunRunning, owner, time.Now())
	return leaseResult(result)
}

func (r *PipelineRepo) CompleteClaimedStep(ctx context.Context, stepRunID, owner string, outputs []model.StepResourceBinding) error {
	paused := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		result := tx.Exec(`UPDATE step_runs AS s SET status = ?, finished_at = ?,
			lease_owner = '', lease_until = NULL, error_code = '', error_message = ''
			FROM tasks AS t WHERE s.task_id = t.id AND s.id = ? AND s.status = ?
			AND s.lease_owner = ? AND s.lease_until > ? AND t.status = ?`,
			model.StepRunSucceeded, now, stepRunID, model.StepRunRunning, owner, now, model.TaskStatusRunning)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			status, valid, err := claimedTaskStatus(tx, stepRunID, owner, now)
			if err != nil {
				return err
			}
			if valid && status == model.TaskStatusPausing {
				if err := tx.Exec(`UPDATE step_runs SET status = ?, lease_owner = '', lease_until = NULL
					WHERE id = ? AND status = ? AND lease_owner = ? AND lease_until > ?`,
					model.StepRunPaused, stepRunID, model.StepRunRunning, owner, now).Error; err != nil {
					return err
				}
				var taskID string
				if err := tx.Raw(`SELECT task_id FROM step_runs WHERE id = ?`, stepRunID).Scan(&taskID).Error; err != nil {
					return err
				}
				paused = true
				return settlePausedTask(tx, taskID, now)
			}
			return port.ErrLeaseLost
		}
		return writeStepOutputs(tx, stepRunID, outputs, now)
	})
	if err != nil {
		return err
	}
	if paused {
		return port.ErrPauseRequested
	}
	return nil
}

func (r *PipelineRepo) FailClaimedStep(ctx context.Context, stepRunID, owner, code, message string) error {
	paused := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		var taskID string
		result := tx.Raw(`UPDATE step_runs AS s SET status = ?, finished_at = ?, lease_owner = '', lease_until = NULL,
			error_code = ?, error_message = ? FROM tasks AS t WHERE s.task_id = t.id AND s.id = ?
			AND s.status = ? AND s.lease_owner = ? AND s.lease_until > ? AND t.status = ? RETURNING s.task_id`,
			model.StepRunFailed, now, code, message, stepRunID, model.StepRunRunning, owner, now,
			model.TaskStatusRunning).Scan(&taskID)
		if result.Error != nil {
			return result.Error
		}
		if taskID == "" {
			status, valid, err := claimedTaskStatus(tx, stepRunID, owner, now)
			if err != nil {
				return err
			}
			if valid && status == model.TaskStatusPausing {
				if err := tx.Exec(`UPDATE step_runs SET status = ?, lease_owner = '', lease_until = NULL
					WHERE id = ? AND status = ? AND lease_owner = ? AND lease_until > ?`,
					model.StepRunPaused, stepRunID, model.StepRunRunning, owner, now).Error; err != nil {
					return err
				}
				if err := tx.Raw(`SELECT task_id FROM step_runs WHERE id = ?`, stepRunID).Scan(&taskID).Error; err != nil {
					return err
				}
				paused = true
				return settlePausedTask(tx, taskID, now)
			}
			return port.ErrLeaseLost
		}
		return tx.Exec(`UPDATE tasks SET status = ?, error_message = ?, finished_at = ?, updated_at = ?
			WHERE id = ? AND status = ?`, model.TaskStatusFailed, message, now, now,
			taskID, model.TaskStatusRunning).Error
	})
	if err != nil {
		return err
	}
	if paused {
		return port.ErrPauseRequested
	}
	return nil
}

func (r *PipelineRepo) PauseClaimedStep(ctx context.Context, stepRunID, owner string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		var taskID string
		result := tx.Raw(`UPDATE step_runs AS s SET status = ?, lease_owner = '', lease_until = NULL
			FROM tasks AS t WHERE s.task_id = t.id AND s.id = ? AND s.status = ?
			AND s.lease_owner = ? AND s.lease_until > ? AND t.status = ? RETURNING s.task_id`,
			model.StepRunPaused, stepRunID, model.StepRunRunning, owner, now, model.TaskStatusPausing).Scan(&taskID)
		if result.Error != nil {
			return result.Error
		}
		if taskID == "" {
			return port.ErrLeaseLost
		}
		return settlePausedTask(tx, taskID, now)
	})
}

func (r *PipelineRepo) RecoverExpiredLeases(ctx context.Context) (int64, error) {
	var affected int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		result := tx.Exec(`UPDATE step_runs AS s SET
			status = CASE WHEN t.status = ? THEN ? ELSE ? END,
			lease_owner = '', lease_until = NULL
			FROM tasks AS t WHERE s.task_id = t.id AND s.status = ? AND s.lease_until <= ?`,
			model.TaskStatusPausing, model.StepRunPaused, model.StepRunQueued,
			model.StepRunRunning, now)
		if result.Error != nil {
			return result.Error
		}
		affected = result.RowsAffected
		return tx.Exec(`UPDATE tasks AS t SET status = ?, updated_at = ? WHERE t.status = ?
			AND NOT EXISTS (SELECT 1 FROM step_runs s WHERE s.task_id = t.id AND s.status = ?)`,
			model.TaskStatusPaused, now, model.TaskStatusPausing, model.StepRunRunning).Error
	})
	return affected, err
}

func settlePausedTask(tx *gorm.DB, taskID string, now time.Time) error {
	return tx.Exec(`UPDATE tasks AS t SET status = ?, updated_at = ? WHERE t.id = ? AND t.status = ?
		AND NOT EXISTS (SELECT 1 FROM step_runs s WHERE s.task_id = t.id AND s.status = ?)`,
		model.TaskStatusPaused, now, taskID, model.TaskStatusPausing, model.StepRunRunning).Error
}

func claimedTaskStatus(tx *gorm.DB, stepRunID, owner string, now time.Time) (string, bool, error) {
	var status string
	result := tx.Raw(`SELECT t.status FROM tasks AS t JOIN step_runs AS s ON s.task_id = t.id
		WHERE s.id = ? AND s.status = ? AND s.lease_owner = ? AND s.lease_until > ?`,
		stepRunID, model.StepRunRunning, owner, now).Scan(&status)
	if result.Error != nil {
		return "", false, result.Error
	}
	return status, status != "", nil
}

func writeStepOutputs(tx *gorm.DB, stepRunID string, outputs []model.StepResourceBinding, now time.Time) error {
	for i := range outputs {
		binding := outputs[i]
		if binding.ID == "" {
			binding.ID = uuid.NewString()
		}
		boundary := jsonOrEmpty(binding.Boundary)
		if err := tx.Exec(`INSERT INTO step_resource_bindings
			(id, step_run_id, port_name, resource_type, resource_id, boundary, created_at)
			VALUES (?, ?, ?, ?, ?, ?::jsonb, ?)`, binding.ID, stepRunID, binding.PortName,
			binding.ResourceType, binding.ResourceID, string(boundary), now).Error; err != nil {
			return err
		}
	}
	return nil
}

func jsonOrEmpty(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

func transitionResult(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return port.ErrInvalidTransition
	}
	return nil
}

func leaseResult(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return port.ErrLeaseLost
	}
	return nil
}

type taskDefinitionV2Row struct {
	ID             string    `gorm:"column:id;primaryKey"`
	WorkspaceID    string    `gorm:"column:workspace_id"`
	Key            string    `gorm:"column:key"`
	Name           string    `gorm:"column:name"`
	Description    string    `gorm:"column:description"`
	Status         string    `gorm:"column:status"`
	Definition     string    `gorm:"column:definition;type:jsonb"`
	DefinitionHash string    `gorm:"column:definition_hash"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
}

func (taskDefinitionV2Row) TableName() string { return "task_definitions" }

func (row taskDefinitionV2Row) toModel() (*model.TaskDefinition, error) {
	var definition model.TaskDefinition
	if err := json.Unmarshal([]byte(row.Definition), &definition); err != nil {
		return nil, fmt.Errorf("解析任务定义 %s: %w", row.ID, err)
	}
	definition.ID = row.ID
	definition.WorkspaceID = row.WorkspaceID
	definition.Key = row.Key
	definition.Name = row.Name
	definition.Description = row.Description
	definition.Status = row.Status
	definition.DefinitionHash = row.DefinitionHash
	definition.CreatedAt, definition.UpdatedAt = row.CreatedAt, row.UpdatedAt
	return &definition, nil
}

type taskResourceBindingRow struct {
	ID           string                  `gorm:"column:id;primaryKey"`
	TaskID       string                  `gorm:"column:task_id"`
	PortName     string                  `gorm:"column:port_name"`
	Direction    model.ResourceDirection `gorm:"column:direction"`
	ResourceType model.ResourceType      `gorm:"column:resource_type"`
	ResourceID   string                  `gorm:"column:resource_id"`
	Boundary     string                  `gorm:"column:boundary;type:jsonb"`
	CreatedAt    time.Time               `gorm:"column:created_at"`
}

type stepResourceBindingRow struct {
	ID           string             `gorm:"column:id;primaryKey"`
	StepRunID    string             `gorm:"column:step_run_id"`
	PortName     string             `gorm:"column:port_name"`
	ResourceType model.ResourceType `gorm:"column:resource_type"`
	ResourceID   string             `gorm:"column:resource_id"`
	Boundary     string             `gorm:"column:boundary;type:jsonb"`
	CreatedAt    time.Time          `gorm:"column:created_at"`
}

func (stepResourceBindingRow) TableName() string { return "step_resource_bindings" }

func (row stepResourceBindingRow) toModel() model.StepResourceBinding {
	return model.StepResourceBinding{
		ID: row.ID, StepRunID: row.StepRunID, PortName: row.PortName,
		ResourceType: row.ResourceType, ResourceID: row.ResourceID,
		Boundary: json.RawMessage(row.Boundary), CreatedAt: row.CreatedAt,
	}
}

func (taskResourceBindingRow) TableName() string { return "task_resource_bindings" }

func (row taskResourceBindingRow) toModel() model.TaskResourceBinding {
	return model.TaskResourceBinding{
		ID: row.ID, TaskID: row.TaskID, PortName: row.PortName, Direction: row.Direction,
		ResourceType: row.ResourceType, ResourceID: row.ResourceID,
		Boundary: json.RawMessage(row.Boundary), CreatedAt: row.CreatedAt,
	}
}

type stepRunV2Row struct {
	ID           string         `gorm:"column:id;primaryKey"`
	TaskID       string         `gorm:"column:task_id"`
	StepID       string         `gorm:"column:step_id"`
	Ordinal      int            `gorm:"column:ordinal"`
	Kind         model.StepKind `gorm:"column:kind"`
	Status       string         `gorm:"column:status"`
	Attempt      int            `gorm:"column:attempt"`
	InputHash    string         `gorm:"column:input_hash"`
	ConfigHash   string         `gorm:"column:config_hash"`
	Checkpoint   string         `gorm:"column:checkpoint;type:jsonb"`
	Progress     string         `gorm:"column:progress;type:jsonb"`
	ErrorCode    string         `gorm:"column:error_code"`
	ErrorMessage string         `gorm:"column:error_message"`
	LeaseOwner   string         `gorm:"column:lease_owner"`
	LeaseUntil   *time.Time     `gorm:"column:lease_until"`
	CreatedAt    time.Time      `gorm:"column:created_at"`
	StartedAt    *time.Time     `gorm:"column:started_at"`
	FinishedAt   *time.Time     `gorm:"column:finished_at"`
}

func (stepRunV2Row) TableName() string { return "step_runs" }

func (row stepRunV2Row) toModel() model.StepRun {
	step := model.StepRun{
		ID: row.ID, TaskID: row.TaskID, StepID: row.StepID, Ordinal: row.Ordinal, Kind: row.Kind,
		Status: row.Status, Attempt: row.Attempt, InputHash: row.InputHash,
		ConfigHash: row.ConfigHash, Checkpoint: json.RawMessage(row.Checkpoint),
		Progress: json.RawMessage(row.Progress), ErrorCode: row.ErrorCode,
		ErrorMessage: row.ErrorMessage, LeaseOwner: row.LeaseOwner, CreatedAt: row.CreatedAt,
	}
	if row.LeaseUntil != nil {
		step.LeaseUntil = *row.LeaseUntil
	}
	if row.StartedAt != nil {
		step.StartedAt = *row.StartedAt
	}
	if row.FinishedAt != nil {
		step.FinishedAt = *row.FinishedAt
	}
	return step
}
