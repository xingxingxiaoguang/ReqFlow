package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"reqflow/internal/domain/model"
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
