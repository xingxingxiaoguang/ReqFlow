package port

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"reqflow/internal/domain/model"
)

var (
	// ErrNoRunnableStep 表示队列当前为空；Worker 应等待下一轮，不视为故障。
	ErrNoRunnableStep = errors.New("no runnable step")
	// ErrLeaseLost 表示写入者已不再拥有有效 lease，必须丢弃本次执行结果。
	ErrLeaseLost = errors.New("step lease lost")
	// ErrPauseRequested 由续租返回，通知远端 Worker 在有效 lease 内落检查点并退出。
	ErrPauseRequested = errors.New("task pause requested")
	// ErrInvalidTransition 表示状态机前置条件不成立。
	ErrInvalidTransition = errors.New("invalid orchestrator transition")
)

// OrchestratorDefinitionRepo 只承载定义和任务执行快照的创建，避免定义用例依赖
// Worker 队列的整套接口。
type OrchestratorDefinitionRepo interface {
	CreateTaskDefinition(ctx context.Context, definition *model.TaskDefinition, snapshot []byte) error
	GetTaskDefinition(ctx context.Context, id string) (*model.TaskDefinition, error)

	CreateTaskExecution(ctx context.Context, task *model.Task, bindings []model.TaskResourceBinding, steps []model.StepRun) error
}

// TaskResourceResolver 在 Task 创建时把用户定位器解析为具体、存在的资源，并固化
// Dataset through_seq / Retrieval source_seq 等读取边界。alias 只在这里解析一次。
type TaskResourceResolver interface {
	ResolveTaskResource(ctx context.Context, workspaceID string, binding model.TaskResourceBinding, alias string) (model.TaskResourceBinding, error)
}

type OrchestratorExecutionReader interface {
	GetTaskExecution(ctx context.Context, taskID string) (*model.TaskExecution, error)
	GetTaskResourceBindings(ctx context.Context, taskID string) ([]model.TaskResourceBinding, error)
	GetStepRuns(ctx context.Context, taskID string) ([]model.StepRun, error)
	GetStepResourceBindings(ctx context.Context, taskID string) ([]model.StepResourceBinding, error)
}

// OrchestratorTaskFilter 是 V2 Task 目录的只读查询条件。V2 与 Legacy 共用 tasks
// 物理表，但查询边界始终以 definition_id 是否存在隔离，避免两个运行时相互污染。
type OrchestratorTaskFilter struct {
	WorkspaceID string
	Status      string
	Limit       int
}

// OrchestratorTaskQueryRepo 只承担 V2 Task 目录查询，不把读模型能力塞进生命周期写口。
type OrchestratorTaskQueryRepo interface {
	ListOrchestratorTasks(ctx context.Context, filter OrchestratorTaskFilter) ([]model.Task, error)
}

type OrchestratorSchedulerRepo interface {
	OrchestratorExecutionReader
	ListSchedulableTaskIDs(ctx context.Context, limit int) ([]string, error)
	QueueReadySteps(ctx context.Context, taskID string, queuedSteps, awaitingSteps []StepQueueEntry) error
	CompleteTask(ctx context.Context, taskID string, outputs []model.TaskResourceBinding) error
}

// StepQueueEntry 在 pending -> queued/awaiting 时固化本次解析后的输入哈希。
// 定义快照固定 config_hash，二者共同保护 checkpoint 恢复边界。
type StepQueueEntry struct {
	StepRunID string
	InputHash string
}

type OrchestratorLifecycleRepo interface {
	OrchestratorExecutionReader
	StartTask(ctx context.Context, taskID string) error
	RequestTaskPause(ctx context.Context, taskID string) error
	ResumeTask(ctx context.Context, taskID string) error
	RetryStep(ctx context.Context, taskID, stepID string) error
	CompleteAwaitingStep(ctx context.Context, stepRunID string, outputs []model.StepResourceBinding) error
}

// OrchestratorWorkerRepo 是持久化执行队列。所有带 owner 的更新都必须验证
// lease 仍有效，避免过期 Worker 覆盖新 Worker 的结果。
type OrchestratorWorkerRepo interface {
	OrchestratorExecutionReader
	ClaimStep(ctx context.Context, owner string, leaseUntil time.Time) (*model.StepRun, error)
	RenewStepLease(ctx context.Context, stepRunID, owner string, leaseUntil time.Time) error
	SaveStepCheckpoint(ctx context.Context, stepRunID, owner string, checkpoint json.RawMessage) error
	SaveStepProgress(ctx context.Context, stepRunID, owner string, progress json.RawMessage) error
	CompleteClaimedStep(ctx context.Context, stepRunID, owner string, outputs []model.StepResourceBinding) error
	FailClaimedStep(ctx context.Context, stepRunID, owner, code, message string) error
	PauseClaimedStep(ctx context.Context, stepRunID, owner string) error
	RecoverExpiredLeases(ctx context.Context) (int64, error)
}

type OrchestratorRuntimeRepo interface {
	OrchestratorSchedulerRepo
	OrchestratorLifecycleRepo
	OrchestratorWorkerRepo
}

// OrchestratorRepo 是生产仓储实现应满足的完整能力集合。
type OrchestratorRepo interface {
	OrchestratorDefinitionRepo
	OrchestratorRuntimeRepo
}
