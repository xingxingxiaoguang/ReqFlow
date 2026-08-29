package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

func TestWorkerResumesCheckpointAndPersistsTypedOutputs(t *testing.T) {
	definition := model.TaskDefinition{
		Key: "resume_extract", Name: "续跑抽取",
		InputPorts:     map[string]model.PortDefinition{"source": {ResourceType: model.ResourceAssetSet, Required: true}},
		OutputPorts:    map[string]model.PortDefinition{"batch": {ResourceType: model.ResourceDatasetBatch}},
		OutputBindings: map[string]string{"batch": "$step.extract.batch"},
		Steps: []model.StepDefinition{{ID: "extract", Name: "抽取", Kind: model.StepKindLLMExtract,
			Inputs:  map[string]string{"source": "$task.source"},
			Outputs: map[string]model.ResourceType{"batch": model.ResourceDatasetBatch}}},
	}
	snapshot, _, err := logic.NormalizeTaskDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := json.RawMessage(`{"offset":4}`)
	claimed := model.StepRun{ID: "run-1", TaskID: "task-1", StepID: "extract", Kind: model.StepKindLLMExtract,
		Status: model.StepRunRunning, Attempt: 2, Checkpoint: checkpoint}
	repo := &workerMemoryRepo{claimed: claimed, execution: model.TaskExecution{
		Task: model.Task{ID: "task-1", Status: model.TaskStatusRunning, DefinitionSnapshot: string(snapshot)},
		Inputs: []model.TaskResourceBinding{{PortName: "source", Direction: model.ResourceInput,
			ResourceType: model.ResourceAssetSet, ResourceID: "asset-set-1"}},
		Steps: []model.StepRun{claimed},
	}}
	executor := &resumeRecordingExecutor{kind: model.StepKindLLMExtract}
	registry, err := NewRegistry(executor)
	if err != nil {
		t.Fatal(err)
	}
	// Worker 完成后的调度由独立 fake 吸收；本测试只验证执行/lease 边界。
	schedulerRepo := &schedulerMemoryRepo{execution: model.TaskExecution{Task: model.Task{Status: model.TaskStatusPaused}}}
	worker, err := NewWorker(repo, registry, NewScheduler(schedulerRepo), WorkerOptions{
		Owner: "worker-a", LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if executor.executeCalled || !executor.resumeCalled {
		t.Fatalf("attempt=2 且有检查点必须走 Resume: execute=%v resume=%v", executor.executeCalled, executor.resumeCalled)
	}
	if executor.gotCheckpoint != string(checkpoint) || executor.gotInput.ResourceID != "asset-set-1" {
		t.Fatalf("续跑上下文错误: checkpoint=%s input=%+v", executor.gotCheckpoint, executor.gotInput)
	}
	if len(repo.completed) != 1 || repo.completed[0].StepRunID != "run-1" || repo.completed[0].ResourceID != "batch-1" {
		t.Fatalf("步骤输出未按 StepRun 隔离持久化: %+v", repo.completed)
	}
	if len(repo.progress) == 0 {
		t.Fatal("Executor metrics 应持久化到 progress")
	}
}

type resumeRecordingExecutor struct {
	kind          model.StepKind
	executeCalled bool
	resumeCalled  bool
	gotCheckpoint string
	gotInput      model.ResourceRef
}

func (e *resumeRecordingExecutor) Kind() model.StepKind { return e.kind }
func (*resumeRecordingExecutor) ValidateDefinition(context.Context, model.StepDefinition) error {
	return nil
}
func (e *resumeRecordingExecutor) Execute(context.Context, StepRunContext) (StepResult, error) {
	e.executeCalled = true
	return StepResult{}, errors.New("Execute 不应被调用")
}
func (e *resumeRecordingExecutor) Resume(_ context.Context, run StepRunContext, checkpoint json.RawMessage) (StepResult, error) {
	e.resumeCalled = true
	e.gotCheckpoint = string(checkpoint)
	e.gotInput = run.Inputs["source"]
	return StepResult{
		Outputs: map[string]model.ResourceRef{"batch": {
			ResourceType: model.ResourceDatasetBatch, ResourceID: "batch-1",
		}},
		Metrics: map[string]any{"records": 4},
	}, nil
}

type workerMemoryRepo struct {
	claimed   model.StepRun
	execution model.TaskExecution
	completed []model.StepResourceBinding
	progress  json.RawMessage
}

func (r *workerMemoryRepo) GetTaskExecution(context.Context, string) (*model.TaskExecution, error) {
	clone := r.execution
	clone.Inputs = append([]model.TaskResourceBinding(nil), r.execution.Inputs...)
	clone.Steps = append([]model.StepRun(nil), r.execution.Steps...)
	return &clone, nil
}
func (r *workerMemoryRepo) GetTaskResourceBindings(context.Context, string) ([]model.TaskResourceBinding, error) {
	return append([]model.TaskResourceBinding(nil), r.execution.Inputs...), nil
}
func (r *workerMemoryRepo) GetStepRuns(context.Context, string) ([]model.StepRun, error) {
	return append([]model.StepRun(nil), r.execution.Steps...), nil
}
func (r *workerMemoryRepo) GetStepResourceBindings(context.Context, string) ([]model.StepResourceBinding, error) {
	return nil, nil
}
func (r *workerMemoryRepo) ClaimStep(context.Context, string, time.Time) (*model.StepRun, error) {
	clone := r.claimed
	return &clone, nil
}
func (*workerMemoryRepo) RenewStepLease(context.Context, string, string, time.Time) error {
	return nil
}
func (*workerMemoryRepo) SaveStepCheckpoint(context.Context, string, string, json.RawMessage) error {
	return nil
}
func (r *workerMemoryRepo) SaveStepProgress(_ context.Context, _, _ string, progress json.RawMessage) error {
	r.progress = append([]byte(nil), progress...)
	return nil
}
func (r *workerMemoryRepo) CompleteClaimedStep(_ context.Context, _, _ string, outputs []model.StepResourceBinding) error {
	r.completed = append([]model.StepResourceBinding(nil), outputs...)
	return nil
}
func (*workerMemoryRepo) FailClaimedStep(context.Context, string, string, string, string) error {
	return nil
}
func (*workerMemoryRepo) PauseClaimedStep(context.Context, string, string) error { return nil }
func (*workerMemoryRepo) RecoverExpiredLeases(context.Context) (int64, error)    { return 0, nil }

var _ port.OrchestratorWorkerRepo = (*workerMemoryRepo)(nil)
