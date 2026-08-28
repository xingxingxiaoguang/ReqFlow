package port

import (
	"context"

	"reqflow/internal/domain/model"
)

// OrchestratorRepo 持久化任务定义快照、资源端口和 StepRun。
type OrchestratorRepo interface {
	CreateTaskDefinition(ctx context.Context, definition *model.TaskDefinition, snapshot []byte) error
	GetTaskDefinition(ctx context.Context, id string) (*model.TaskDefinition, error)

	CreateTaskExecution(ctx context.Context, task *model.Task, bindings []model.TaskResourceBinding, steps []model.StepRun) error
	GetTaskResourceBindings(ctx context.Context, taskID string) ([]model.TaskResourceBinding, error)
	GetStepRuns(ctx context.Context, taskID string) ([]model.StepRun, error)
}
