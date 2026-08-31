package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

func TestWorkerPoolHonorsConcurrencyLimit(t *testing.T) {
	const concurrency = 3
	repo := newPoolWorkerRepo(t, 4, false)
	executor := &blockingPoolExecutor{
		kind: model.StepKindLLMExtract, started: make(chan string, 4), release: make(chan struct{}),
	}
	registry, err := NewRegistry(executor)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorker(repo, registry, NewScheduler(repo), WorkerOptions{
		Owner: "pool-test", Concurrency: concurrency, LeaseDuration: time.Second,
		PollInterval: 10 * time.Millisecond, RecoveryInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	for i := 0; i < concurrency; i++ {
		select {
		case <-executor.started:
		case <-time.After(time.Second):
			t.Fatalf("等待第 %d 个并发槽启动超时", i+1)
		}
	}
	select {
	case taskID := <-executor.started:
		t.Fatalf("并发上限为 %d 时提前启动了额外任务 %s", concurrency, taskID)
	case <-time.After(50 * time.Millisecond):
	}
	if got := executor.maxRunning.Load(); got != concurrency {
		t.Fatalf("最大实际并发数错误: got %d want %d", got, concurrency)
	}

	close(executor.release)
	waitForPoolCount(t, time.Second, repo.completedCount, 4, "全部任务完成")
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Worker Pool 退出错误: %v", err)
	}
}

func TestWorkerPoolCancelTaskStopsAllActiveSteps(t *testing.T) {
	repo := newPoolWorkerRepo(t, 2, true)
	executor := &blockingPoolExecutor{
		kind: model.StepKindLLMExtract, started: make(chan string, 2), release: make(chan struct{}),
	}
	registry, err := NewRegistry(executor)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorker(repo, registry, NewScheduler(repo), WorkerOptions{
		Owner: "pause-test", Concurrency: 2, LeaseDuration: time.Second,
		PollInterval: 10 * time.Millisecond, RecoveryInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	for i := 0; i < 2; i++ {
		select {
		case <-executor.started:
		case <-time.After(time.Second):
			t.Fatalf("等待并行步骤 %d 启动超时", i+1)
		}
	}
	if !worker.CancelTask("task-shared") {
		t.Fatal("暂停应命中当前任务的全部本地运行步骤")
	}
	waitForPoolCount(t, time.Second, repo.pausedCount, 2, "全部并行步骤暂停")
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Worker Pool 退出错误: %v", err)
	}
}

type blockingPoolExecutor struct {
	kind       model.StepKind
	started    chan string
	release    chan struct{}
	running    atomic.Int32
	maxRunning atomic.Int32
}

func (e *blockingPoolExecutor) Kind() model.StepKind { return e.kind }
func (*blockingPoolExecutor) ValidateDefinition(context.Context, model.StepDefinition) error {
	return nil
}
func (e *blockingPoolExecutor) Execute(ctx context.Context, run StepRunContext) (StepResult, error) {
	running := e.running.Add(1)
	defer e.running.Add(-1)
	for {
		maximum := e.maxRunning.Load()
		if running <= maximum || e.maxRunning.CompareAndSwap(maximum, running) {
			break
		}
	}
	e.started <- run.TaskID + ":" + run.StepID
	select {
	case <-ctx.Done():
		return StepResult{}, ctx.Err()
	case <-e.release:
		return StepResult{Outputs: map[string]model.ResourceRef{"batch": {
			ResourceType: model.ResourceDatasetBatch, ResourceID: "batch-" + run.StepRunID,
		}}}, nil
	}
}
func (e *blockingPoolExecutor) Resume(ctx context.Context, run StepRunContext, _ json.RawMessage) (StepResult, error) {
	return e.Execute(ctx, run)
}

type poolWorkerRepo struct {
	mu         sync.Mutex
	claims     []model.StepRun
	executions map[string]model.TaskExecution
	completed  int
	paused     int
}

func newPoolWorkerRepo(t *testing.T, count int, sharedTask bool) *poolWorkerRepo {
	t.Helper()
	repo := &poolWorkerRepo{executions: make(map[string]model.TaskExecution)}
	if sharedTask {
		steps := make([]model.StepDefinition, count)
		runs := make([]model.StepRun, count)
		for i := 0; i < count; i++ {
			stepID := fmt.Sprintf("extract_%d", i+1)
			steps[i] = poolStepDefinition(stepID)
			runs[i] = model.StepRun{ID: fmt.Sprintf("run-%d", i+1), TaskID: "task-shared", StepID: stepID,
				Ordinal: i + 1, Kind: model.StepKindLLMExtract, Status: model.StepRunRunning}
		}
		repo.claims = append(repo.claims, runs...)
		repo.executions["task-shared"] = poolTaskExecution(t, "task-shared", steps, runs)
		return repo
	}
	for i := 0; i < count; i++ {
		taskID := fmt.Sprintf("task-%d", i+1)
		run := model.StepRun{ID: fmt.Sprintf("run-%d", i+1), TaskID: taskID, StepID: "extract",
			Ordinal: 1, Kind: model.StepKindLLMExtract, Status: model.StepRunRunning}
		repo.claims = append(repo.claims, run)
		repo.executions[taskID] = poolTaskExecution(t, taskID, []model.StepDefinition{poolStepDefinition("extract")}, []model.StepRun{run})
	}
	return repo
}

func poolStepDefinition(stepID string) model.StepDefinition {
	return model.StepDefinition{ID: stepID, Name: stepID, Kind: model.StepKindLLMExtract,
		Inputs:  map[string]string{"source": "$task.source"},
		Outputs: map[string]model.ResourceType{"batch": model.ResourceDatasetBatch}}
}

func poolTaskExecution(t *testing.T, taskID string, steps []model.StepDefinition, runs []model.StepRun) model.TaskExecution {
	t.Helper()
	definition := model.TaskDefinition{Key: "pool_test", Name: taskID, Status: model.TaskDefinitionActive,
		InputPorts: map[string]model.PortDefinition{"source": {ResourceType: model.ResourceAssetSet, Required: true}},
		Steps:      steps}
	snapshot, _, err := logic.NormalizeTaskDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	return model.TaskExecution{
		Task: model.Task{ID: taskID, Status: model.TaskStatusRunning, DefinitionSnapshot: string(snapshot)},
		Inputs: []model.TaskResourceBinding{{PortName: "source", Direction: model.ResourceInput,
			ResourceType: model.ResourceAssetSet, ResourceID: "asset-set-1"}},
		Steps: runs,
	}
}

func (r *poolWorkerRepo) GetTaskExecution(_ context.Context, taskID string) (*model.TaskExecution, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	execution, ok := r.executions[taskID]
	if !ok {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	clone := execution
	clone.Inputs = append([]model.TaskResourceBinding(nil), execution.Inputs...)
	clone.Steps = append([]model.StepRun(nil), execution.Steps...)
	return &clone, nil
}
func (r *poolWorkerRepo) GetTaskResourceBindings(ctx context.Context, taskID string) ([]model.TaskResourceBinding, error) {
	execution, err := r.GetTaskExecution(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return execution.Inputs, nil
}
func (r *poolWorkerRepo) GetStepRuns(ctx context.Context, taskID string) ([]model.StepRun, error) {
	execution, err := r.GetTaskExecution(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return execution.Steps, nil
}
func (*poolWorkerRepo) GetStepResourceBindings(context.Context, string) ([]model.StepResourceBinding, error) {
	return nil, nil
}
func (r *poolWorkerRepo) ClaimStep(context.Context, string, time.Time) (*model.StepRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.claims) == 0 {
		return nil, port.ErrNoRunnableStep
	}
	claimed := r.claims[0]
	r.claims = r.claims[1:]
	return &claimed, nil
}
func (*poolWorkerRepo) RenewStepLease(context.Context, string, string, time.Time) error {
	return nil
}
func (*poolWorkerRepo) SaveStepCheckpoint(context.Context, string, string, json.RawMessage) error {
	return nil
}
func (*poolWorkerRepo) SaveStepProgress(context.Context, string, string, json.RawMessage) error {
	return nil
}
func (r *poolWorkerRepo) CompleteClaimedStep(context.Context, string, string, []model.StepResourceBinding) error {
	r.mu.Lock()
	r.completed++
	r.mu.Unlock()
	return nil
}
func (*poolWorkerRepo) FailClaimedStep(context.Context, string, string, string, string) error {
	return nil
}
func (r *poolWorkerRepo) PauseClaimedStep(context.Context, string, string) error {
	r.mu.Lock()
	r.paused++
	r.mu.Unlock()
	return nil
}
func (*poolWorkerRepo) RecoverExpiredLeases(context.Context) (int64, error) { return 0, nil }
func (*poolWorkerRepo) ListSchedulableTaskIDs(context.Context, int) ([]string, error) {
	return nil, nil
}
func (*poolWorkerRepo) QueueReadySteps(context.Context, string, []port.StepQueueEntry, []port.StepQueueEntry) error {
	return nil
}
func (*poolWorkerRepo) CompleteTask(context.Context, string, []model.TaskResourceBinding) error {
	return nil
}

func (r *poolWorkerRepo) completedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.completed
}

func (r *poolWorkerRepo) pausedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paused
}

func waitForPoolCount(t *testing.T, timeout time.Duration, current func() int, expected int, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if current() == expected {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待%s超时: got %d want %d", description, current(), expected)
}

var _ port.OrchestratorWorkerRepo = (*poolWorkerRepo)(nil)
var _ port.OrchestratorSchedulerRepo = (*poolWorkerRepo)(nil)
