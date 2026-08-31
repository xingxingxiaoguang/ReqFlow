package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type WorkerOptions struct {
	Owner            string
	Concurrency      int
	LeaseDuration    time.Duration
	PollInterval     time.Duration
	RecoveryInterval time.Duration
	ReconcileLimit   int
}

type activeRun struct {
	cancel         context.CancelFunc
	pauseRequested bool
}

// Worker 从 PostgreSQL 领取 StepRun。进程内 active 只用于降低本机暂停延迟，
// 不是真相源；崩溃恢复完全依赖 lease + checkpoint。
type Worker struct {
	repo      port.OrchestratorWorkerRepo
	registry  *Registry
	scheduler *Scheduler
	opts      WorkerOptions

	mu     sync.Mutex
	active map[string]map[string]*activeRun // task_id -> step_run_id -> 当前本机运行
}

func NewWorker(repo port.OrchestratorWorkerRepo, registry *Registry, scheduler *Scheduler, opts WorkerOptions) (*Worker, error) {
	if repo == nil {
		return nil, fmt.Errorf("orchestrator: worker repo is required")
	}
	if registry == nil {
		return nil, fmt.Errorf("orchestrator: executor registry is required")
	}
	if scheduler == nil {
		return nil, fmt.Errorf("orchestrator: scheduler is required")
	}
	if opts.Owner == "" {
		opts.Owner = randomWorkerOwner()
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if opts.LeaseDuration <= 0 {
		opts.LeaseDuration = 30 * time.Second
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 500 * time.Millisecond
	}
	if opts.RecoveryInterval <= 0 {
		opts.RecoveryInterval = 5 * time.Second
	}
	if opts.ReconcileLimit <= 0 {
		opts.ReconcileLimit = 100
	}
	return &Worker{repo: repo, registry: registry, scheduler: scheduler, opts: opts,
		active: map[string]map[string]*activeRun{}}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	if _, err := w.repo.RecoverExpiredLeases(ctx); err != nil {
		return fmt.Errorf("回收过期 lease: %w", err)
	}
	if err := w.reconcile(ctx); err != nil {
		return err
	}

	poolCtx, stopPool := context.WithCancel(ctx)
	var pool sync.WaitGroup
	errorsFromPool := make(chan error, w.opts.Concurrency)
	for range w.opts.Concurrency {
		pool.Add(1)
		go func() {
			defer pool.Done()
			errorsFromPool <- w.runSlot(poolCtx)
		}()
	}
	defer func() {
		stopPool()
		pool.Wait()
	}()

	recovery := time.NewTicker(w.opts.RecoveryInterval)
	defer recovery.Stop()
	reconcile := time.NewTicker(w.opts.PollInterval)
	defer reconcile.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errorsFromPool:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		case <-reconcile.C:
			if err := w.reconcile(ctx); err != nil {
				return err
			}
		case <-recovery.C:
			if _, err := w.repo.RecoverExpiredLeases(ctx); err != nil {
				return fmt.Errorf("回收过期 lease: %w", err)
			}
		}
	}
}

func (w *Worker) runSlot(ctx context.Context) error {
	for {
		err := w.RunOnce(ctx)
		if err == nil {
			continue
		}
		if !errors.Is(err, port.ErrNoRunnableStep) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		timer := time.NewTimer(w.opts.PollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (w *Worker) reconcile(ctx context.Context) error {
	if err := w.scheduler.Reconcile(ctx, w.opts.ReconcileLimit); err != nil && !errors.Is(err, port.ErrInvalidTransition) {
		return err
	}
	return nil
}

func (w *Worker) RunOnce(ctx context.Context) error {
	leaseUntil := time.Now().Add(w.opts.LeaseDuration)
	claimed, err := w.repo.ClaimStep(ctx, w.opts.Owner, leaseUntil)
	if err != nil {
		return err
	}
	execution, err := w.repo.GetTaskExecution(ctx, claimed.TaskID)
	if err != nil {
		return err // 保留 lease，周期回收后重试
	}
	if execution.Task.Status == model.TaskStatusPausing {
		return w.repo.PauseClaimedStep(ctx, claimed.ID, w.opts.Owner)
	}
	definition, err := executionDefinition(execution)
	if err != nil {
		return w.failClaim(ctx, claimed, "invalid_definition_snapshot", err)
	}
	step, ok := findStepDefinition(definition, claimed.StepID)
	if !ok || step.Kind != claimed.Kind {
		return w.failClaim(ctx, claimed, "step_definition_mismatch", fmt.Errorf("步骤 %s 与定义快照不一致", claimed.StepID))
	}
	executor, err := w.registry.Lookup(claimed.Kind)
	if err != nil {
		return w.failClaim(ctx, claimed, "executor_not_registered", err)
	}
	inputs, err := resolveStepInputs(step, execution)
	if err != nil {
		return w.failClaim(ctx, claimed, "input_resolution_failed", err)
	}

	execCtx, cancel := context.WithCancel(ctx)
	active := &activeRun{cancel: cancel}
	w.mu.Lock()
	if w.active[claimed.TaskID] == nil {
		w.active[claimed.TaskID] = make(map[string]*activeRun)
	}
	w.active[claimed.TaskID][claimed.ID] = active
	w.mu.Unlock()
	defer func() {
		cancel()
		w.mu.Lock()
		delete(w.active[claimed.TaskID], claimed.ID)
		if len(w.active[claimed.TaskID]) == 0 {
			delete(w.active, claimed.TaskID)
		}
		w.mu.Unlock()
	}()

	runCtx := StepRunContext{
		TaskID: claimed.TaskID, StepRunID: claimed.ID, StepID: claimed.StepID,
		Attempt: claimed.Attempt, InputHash: claimed.InputHash, ConfigHash: claimed.ConfigHash,
		IdempotencyKey: claimed.TaskID + ":" + claimed.StepID,
		ExecutionKey: fmt.Sprintf("%s:%s:%s:%s:%d", claimed.TaskID, claimed.StepID,
			claimed.InputHash, claimed.ConfigHash, claimed.Attempt),
		Inputs: inputs, Config: step.Config,
		Checkpoint: ownedCheckpointWriter{repo: w.repo, stepRunID: claimed.ID, owner: w.opts.Owner},
		Progress:   ownedProgressReporter{repo: w.repo, stepRunID: claimed.ID, owner: w.opts.Owner},
	}

	stopRenew := make(chan struct{})
	renewDone := make(chan error, 1)
	go w.renewLoop(execCtx, cancel, claimed.ID, stopRenew, renewDone)

	var result StepResult
	if claimed.Attempt > 1 && hasCheckpoint(claimed.Checkpoint) {
		result, err = executor.Resume(execCtx, runCtx, claimed.Checkpoint)
	} else {
		result, err = executor.Execute(execCtx, runCtx)
	}
	close(stopRenew)
	renewErr := <-renewDone

	w.mu.Lock()
	pauseRequested := active.pauseRequested
	w.mu.Unlock()
	if pauseRequested || errors.Is(renewErr, port.ErrPauseRequested) {
		if pauseErr := w.repo.PauseClaimedStep(context.WithoutCancel(ctx), claimed.ID, w.opts.Owner); pauseErr != nil {
			return pauseErr
		}
		return nil
	}
	if renewErr != nil {
		return renewErr
	}
	if ctx.Err() != nil {
		return ctx.Err() // 进程关闭：不改状态，交给 lease 回收
	}
	if err != nil {
		return w.failClaim(ctx, claimed, "executor_failed", err)
	}
	outputs, err := validateOutputs(step, result)
	if err != nil {
		return w.failClaim(ctx, claimed, "invalid_step_outputs", err)
	}
	for i := range outputs {
		outputs[i].StepRunID = claimed.ID
	}
	if len(result.Metrics) > 0 {
		if raw, marshalErr := json.Marshal(map[string]any{"metrics": result.Metrics}); marshalErr == nil {
			if err := w.repo.SaveStepProgress(ctx, claimed.ID, w.opts.Owner, raw); err != nil {
				return err
			}
		}
	}
	if err := w.repo.CompleteClaimedStep(ctx, claimed.ID, w.opts.Owner, outputs); err != nil {
		if errors.Is(err, port.ErrPauseRequested) {
			return nil
		}
		return err
	}
	return w.scheduler.Schedule(ctx, claimed.TaskID)
}

func (w *Worker) CancelTask(taskID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	runs := w.active[taskID]
	if len(runs) == 0 {
		return false
	}
	for _, run := range runs {
		run.pauseRequested = true
		run.cancel()
	}
	return true
}

func (w *Worker) renewLoop(ctx context.Context, cancel context.CancelFunc, stepRunID string, stop <-chan struct{}, done chan<- error) {
	interval := w.opts.LeaseDuration / 3
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			done <- nil
			return
		case <-ctx.Done():
			// 由 RunOnce 判断是显式暂停还是进程关闭。
			done <- nil
			return
		case <-ticker.C:
			err := w.repo.RenewStepLease(context.WithoutCancel(ctx), stepRunID, w.opts.Owner, time.Now().Add(w.opts.LeaseDuration))
			if err != nil {
				cancel()
				done <- err
				return
			}
		}
	}
}

func (w *Worker) failClaim(ctx context.Context, claimed *model.StepRun, code string, cause error) error {
	if err := w.repo.FailClaimedStep(context.WithoutCancel(ctx), claimed.ID, w.opts.Owner, code, cause.Error()); err != nil {
		if errors.Is(err, port.ErrPauseRequested) {
			return nil
		}
		return errors.Join(cause, err)
	}
	return nil
}

type ownedCheckpointWriter struct {
	repo      port.OrchestratorWorkerRepo
	stepRunID string
	owner     string
}

func (w ownedCheckpointWriter) Save(ctx context.Context, checkpoint json.RawMessage) error {
	return w.repo.SaveStepCheckpoint(ctx, w.stepRunID, w.owner, checkpoint)
}

type ownedProgressReporter struct {
	repo      port.OrchestratorWorkerRepo
	stepRunID string
	owner     string
}

func (r ownedProgressReporter) Report(ctx context.Context, progress json.RawMessage) error {
	return r.repo.SaveStepProgress(ctx, r.stepRunID, r.owner, progress)
}

func findStepDefinition(definition model.TaskDefinition, stepID string) (model.StepDefinition, bool) {
	for _, step := range definition.Steps {
		if step.ID == stepID {
			return step, true
		}
	}
	return model.StepDefinition{}, false
}

func hasCheckpoint(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return false
	}
	if object, ok := value.(map[string]any); ok {
		return len(object) > 0
	}
	return true
}

func randomWorkerOwner() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return "worker-" + hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("worker-%d", time.Now().UnixNano())
}
